package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"

	"github.com/robfig/cron/v3"
)

type tokenTelemetryDB struct {
	*sql.DB
	blocked bool
	started chan context.Context
	result  chan error
	release chan struct{}
}

func (d *tokenTelemetryDB) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	if !strings.Contains(query, "-- name: TouchApiTokenLastUsed") {
		return d.DB.ExecContext(ctx, query, args...)
	}
	d.started <- ctx
	if d.blocked {
		select {
		case <-ctx.Done():
			d.result <- ctx.Err()
			return nil, ctx.Err()
		case <-d.release:
			d.result <- context.Canceled
			return nil, context.Canceled
		}
	}
	result, err := d.DB.ExecContext(ctx, query, args...)
	d.result <- err
	return result, err
}

func TestAPITokenTelemetryE2E(t *testing.T) {
	for _, blocked := range []bool{false, true} {
		name := "update succeeds"
		if blocked {
			name = "database waits for cancellation"
		}
		t.Run(name, func(t *testing.T) {
			// Given: a stored token and the production API router.
			h := newAPIHarness(t)
			user := seedAPIUser(t, h.repo, "telemetry@example.com", "admin")
			_, token := seedAPIToken(t, h.repo, user.ID)
			probe := &tokenTelemetryDB{
				DB: h.repo.DB, blocked: blocked,
				started: make(chan context.Context, 1),
				result:  make(chan error, 1),
				release: make(chan struct{}),
			}
			defer close(probe.release)
			h.repo.Queries = db.New(probe)
			router := server.NewRouter(
				h.repo,
				h.runner,
				cron.NewParser(
					cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
				),
				handler.NewAuthHandler(h.repo),
			)
			srv := httptest.NewServer(router)
			defer srv.Close()
			client := srv.Client()
			client.Timeout = 2 * time.Second
			request, err := http.NewRequestWithContext(
				t.Context(), http.MethodGet, srv.URL+"/api/v1/users/me", nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+token)
			// When: a real authenticated HTTP request starts usage telemetry.
			response, err := client.Do(request)
			if err != nil {
				t.Fatalf("API waited for telemetry: %v", err)
			}
			defer response.Body.Close()
			var actual db.User
			if err := json.NewDecoder(response.Body).Decode(&actual); err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusOK || actual.ID != user.ID {
				t.Fatalf(
					"API status=%d user=%d",
					response.StatusCode,
					actual.ID,
				)
			}
			// Then: the query has its own five-second deadline and releases it.
			var queryContext context.Context
			select {
			case queryContext = <-probe.started:
			case <-time.After(time.Second):
				t.Fatal("telemetry did not start")
			}
			deadline, ok := queryContext.Deadline()
			if !ok || time.Until(deadline) > 5*time.Second ||
				time.Until(deadline) < 3*time.Second {
				t.Fatal("telemetry requires an independent five-second timeout")
			}
			select {
			case err := <-probe.result:
				if blocked && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("blocked query exit: %v", err)
				}
				if !blocked && err != nil {
					t.Fatalf("usage update failed: %v", err)
				}
			case <-time.After(6 * time.Second):
				t.Fatal("telemetry remained blocked")
			}
			select {
			case <-queryContext.Done():
			case <-time.After(time.Second):
				t.Fatal("telemetry context was not released")
			}
			if !blocked {
				var lastUsed sql.NullInt64
				err := h.repo.DB.QueryRowContext(t.Context(),
					"SELECT last_used_at FROM api_tokens WHERE user_id = ?",
					user.ID).Scan(&lastUsed)
				if err != nil || !lastUsed.Valid {
					t.Fatalf("usage timestamp missing: %v", err)
				}
			}
		})
	}
}

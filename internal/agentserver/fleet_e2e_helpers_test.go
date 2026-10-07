package agentserver_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func fleetAdminServer(
	t *testing.T,
	f agentFixture,
	pairing ...agentserver.PairingManager,
) *httptest.Server {
	t.Helper()
	// SQLite :memory: belongs to one connection shared by both listeners.
	f.repo.DB.SetMaxOpenConns(1)
	for _, role := range []string{"admin", "viewer", "deployer"} {
		user, err := f.repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
			Email:        role + "@fleet.test",
			Name:         role,
			PasswordHash: "unused",
			Role:         role,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.repo.Queries.CreateApiToken(
			t.Context(),
			db.CreateApiTokenParams{
				ID:          role,
				UserID:      user.ID,
				Name:        role,
				TokenPrefix: "fleet",
				TokenHash: auth.HashApiToken(
					"ddp_pat_fleet_" + role,
				),
				Scope: "global",
			},
		); err != nil {
			t.Fatal(err)
		}
		if _, err := f.repo.Queries.CreateSession(
			t.Context(),
			db.CreateSessionParams{
				ID: "fleet-" + role, UserID: user.ID, CsrfToken: "csrf",
				ExpiresAt: time.Now().Add(time.Hour).Unix(),
			},
		); err != nil {
			t.Fatal(err)
		}
	}
	rnr := runner.New(f.repo, f.broker)
	var manager agentserver.PairingManager
	if len(pairing) > 0 {
		manager = pairing[0]
	}
	srv := httptest.NewServer(server.NewRouterWithAgentManagement(
		f.repo,
		rnr,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(f.repo),
		manager,
		false,
	))
	t.Cleanup(srv.Close)
	return srv
}

func fleetRequest(
	t *testing.T,
	srv *httptest.Server,
	method, path, role, body string,
	want int,
) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(),
		method,
		srv.URL+path,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ddp_pat_fleet_"+role)
	req.Header.Set("Content-Type", "application/json")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	contents, err := io.ReadAll(res.Body)
	if err != nil || res.StatusCode != want {
		t.Fatalf(
			"%s %s status=%d want=%d body=%s error=%v",
			method,
			path,
			res.StatusCode,
			want,
			contents,
			err,
		)
	}
	t.Logf("%s %s status=%d", method, path, res.StatusCode)
	return contents
}

type fleetState struct {
	Agent struct {
		AdministrativeStatus string `json:"administrative_status"`
		Draining             bool   `json:"draining"`
		Queued               int64  `json:"queued_work"`
		Current              []struct {
			ID int64 `json:"deployment_id"`
		} `json:"current_work"`
		Success *struct {
			ID int64 `json:"deployment_id"`
		} `json:"last_successful_deployment"`
		Health string `json:"health"`
	} `json:"agent"`
}

func fleetAgentState(t *testing.T, srv *httptest.Server) fleetState {
	t.Helper()
	var result fleetState
	if err := json.Unmarshal(
		fleetRequest(
			t,
			srv,
			"GET",
			"/api/v1/admin/agents/test-agent",
			"admin",
			"",
			200,
		),
		&result,
	); err != nil {
		t.Fatal(err)
	}
	return result
}

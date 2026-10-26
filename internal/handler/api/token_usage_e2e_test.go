package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

type tokenUsageProbe struct {
	*sql.DB
	release             chan struct{}
	fail                bool
	calls, active, peak atomic.Int64
	finished            chan error
	contexts            chan context.Context
}

func (p *tokenUsageProbe) ExecContext(
	ctx context.Context,
	query string,
	args ...any,
) (sql.Result, error) {
	if !strings.Contains(query, "-- name: TouchApiTokenLastUsed") {
		return p.DB.ExecContext(ctx, query, args...)
	}
	p.calls.Add(1)
	p.contexts <- ctx
	active := p.active.Add(1)
	for old := p.peak.Load(); active > old; old = p.peak.Load() {
		if p.peak.CompareAndSwap(old, active) {
			break
		}
	}
	defer p.active.Add(-1)
	select {
	case <-ctx.Done():
		p.finished <- ctx.Err()
		return nil, ctx.Err()
	case <-p.release:
	}
	if p.fail {
		err := errors.New("injected telemetry failure")
		p.finished <- err
		return nil, err
	}
	result, err := p.DB.ExecContext(ctx, query, args...)
	p.finished <- err
	return result, err
}

type tokenUsageFixture struct {
	repo                    *repository.Repository
	probe                   *tokenUsageProbe
	srv                     *httptest.Server
	tokenID, token, session string
	userID                  int64
}

func newTokenUsageFixture(t *testing.T, fail bool) *tokenUsageFixture {
	t.Helper()
	return newConfiguredTokenUsageFixture(t, fail, config.Defaults())
}

func newConfiguredTokenUsageFixture(
	t *testing.T, fail bool, settings config.Config,
) *tokenUsageFixture {
	t.Helper()
	h := newAPIHarness(t)
	h.repo = repository.NewConfigured(h.repo.DB, settings)
	h.repo.DB.SetMaxOpenConns(1)
	u := seedAPIUser(t, h.repo, "usage-load@example.test", "admin")
	id, token := seedAPIToken(t, h.repo, u.ID)
	probe := &tokenUsageProbe{
		DB:       h.repo.DB,
		release:  make(chan struct{}),
		fail:     fail,
		finished: make(chan error, 512),
		contexts: make(chan context.Context, 512),
	}
	h.repo.Queries = db.New(probe)
	srv := httptest.NewServer(
		server.NewRouter(
			h.repo,
			h.runner,
			cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
			handler.NewAuthHandler(h.repo),
		),
	)
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-probe.release:
		default:
			close(probe.release)
		}
	})
	srv.Client().Timeout = 2 * time.Second
	return &tokenUsageFixture{
		repo:    h.repo,
		probe:   probe,
		srv:     srv,
		tokenID: id,
		token:   token,
		userID:  u.ID,
		session: memberRoleSession(t, h, u.ID),
	}
}

func (f *tokenUsageFixture) get(
	t *testing.T,
	token, path string,
	want int,
) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(),
		"GET",
		f.srv.URL+path,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	}
	res, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != want {
		t.Fatalf("GET %s status=%d want=%d", path, res.StatusCode, want)
	}
	return body
}

func (f *tokenUsageFixture) burst(t *testing.T, tokens []string) {
	t.Helper()
	var group sync.WaitGroup
	results := make(chan error, len(tokens))
	for _, token := range tokens {
		group.Go(func() {
			req, err := http.NewRequestWithContext(
				t.Context(),
				"GET",
				f.srv.URL+"/api/v1/users/me",
				nil,
			)
			if err != nil {
				results <- err
				return
			}
			req.Header.Set("Authorization", "Bearer "+token)
			res, err := f.srv.Client().Do(req)
			if err != nil {
				results <- err
				return
			}
			defer res.Body.Close()
			_, err = io.Copy(io.Discard, res.Body)
			if err != nil {
				results <- err
				return
			}
			if res.StatusCode != 200 {
				results <- fmt.Errorf("burst status=%d", res.StatusCode)
			}
		})
	}
	group.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func waitTokenUsage(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !ready() {
		if time.Now().After(deadline) {
			t.Fatal("telemetry condition did not complete")
		}
		time.Sleep(time.Millisecond)
	}
}

func waitTokenUsageWrite(t *testing.T, probe *tokenUsageProbe) error {
	t.Helper()
	// Allow the production five-second query deadline and scheduling slack.
	select {
	case err := <-probe.finished:
		return err
	case <-time.After(6 * time.Second):
		t.Fatal("usage write did not finish within its bounded wait")
		return nil
	}
}

func testTokenUsageBurst(t *testing.T) {
	f := newTokenUsageFixture(t, false)
	tokens := make([]string, 256)
	for i := range tokens {
		tokens[i] = f.token
	}
	f.burst(t, tokens)
	waitTokenUsage(t, func() bool { return f.probe.calls.Load() > 0 })
	if f.probe.calls.Load() != 1 || f.probe.peak.Load() != 1 {
		t.Fatalf(
			"same-token calls=%d peak=%d",
			f.probe.calls.Load(),
			f.probe.peak.Load(),
		)
	}
	close(f.probe.release)
	if err := waitTokenUsageWrite(t, f.probe); err != nil {
		t.Fatal(err)
	}
	row, err := f.repo.Queries.GetApiTokenByID(t.Context(), f.tokenID)
	if err != nil || !row.LastUsedAt.Valid ||
		time.Now().Unix()-row.LastUsedAt.Int64 > 60 {
		t.Fatalf("recent usage=%+v error=%v", row.LastUsedAt, err)
	}
	f.burst(t, tokens)
	if f.probe.calls.Load() != 1 {
		t.Fatal("recent token started repeated telemetry")
	}
	f.checkUsageReports(t, row.LastUsedAt.Int64)
	// Advance the fixture across the minute boundary without a wall-clock wait.
	if _, err := f.repo.DB.ExecContext(t.Context(),
		"UPDATE api_tokens SET last_used_at = ? WHERE id = ?",
		time.Now().Unix()-60, f.tokenID,
	); err != nil {
		t.Fatal(err)
	}
	f.get(t, f.token, "/api/v1/users/me", 200)
	waitTokenUsage(t, func() bool {
		current, err := f.repo.Queries.GetApiTokenByID(t.Context(), f.tokenID)
		return err == nil && current.LastUsedAt.Valid &&
			current.LastUsedAt.Int64 >= row.LastUsedAt.Int64
	})
	// Auth must remain current even while the usage timestamp is recent.
	if err := f.repo.Queries.RevokeApiToken(t.Context(), f.tokenID); err != nil {
		t.Fatal(err)
	}
	f.get(t, f.token, "/api/v1/users/me", 401)
	if f.probe.calls.Load() != 2 {
		t.Fatal("revoked token admitted telemetry")
	}
	t.Log(
		"256 concurrent same-token requests: one telemetry call; repeated recent use: zero extra calls",
	)
}

func (f *tokenUsageFixture) checkUsageReports(t *testing.T, lastUsedAt int64) {
	t.Helper()
	for _, path := range []string{"/api/v1/tokens", "/api/v1/admin/tokens", fmt.Sprintf("/api/v1/admin/tokens?user_id=%d", f.userID)} {
		var response struct {
			Items []struct {
				ID         string `json:"id"`
				LastUsedAt *int64 `json:"last_used_at"`
			}
		}
		if err := json.Unmarshal(f.get(t, f.token, path, 200), &response); err != nil {
			t.Fatal(err)
		}
		found := false
		for _, token := range response.Items {
			if token.ID == f.tokenID {
				found = token.LastUsedAt != nil &&
					*token.LastUsedAt == lastUsedAt
			}
		}
		if !found {
			t.Fatalf("last_used_at missing from %s", path)
		}
	}
	stamp := time.Unix(lastUsedAt, 0).
		Format("2006-01-02 15:04:05")
	for _, path := range []string{"/settings/tokens", "/admin/tokens"} {
		if !strings.Contains(string(f.get(t, "", path, 200)), stamp) {
			t.Fatalf("timestamp missing from %s", path)
		}
	}
}

func testTokenUsageCapacity(t *testing.T) {
	f := newTokenUsageFixture(t, false)
	for range 8 {
		_, token := seedAPIToken(t, f.repo, f.userID)
		f.get(t, token, "/api/v1/users/me", 200)
	}
	waitTokenUsage(t, func() bool { return f.probe.active.Load() == 8 })
	f.burst(t, []string{f.token, f.token, f.token})
	if f.probe.calls.Load() != 8 || f.probe.peak.Load() != 8 {
		t.Fatalf(
			"unbounded calls=%d peak=%d",
			f.probe.calls.Load(),
			f.probe.peak.Load(),
		)
	}
	row, err := f.repo.Queries.GetApiTokenByID(t.Context(), f.tokenID)
	if err != nil || row.LastUsedAt.Valid {
		t.Fatal("overflow token unexpectedly recorded")
	}
	// Revocation still applies when every telemetry slot is blocked.
	revokedID, revoked := seedAPIToken(t, f.repo, f.userID)
	if err := f.repo.Queries.RevokeApiToken(t.Context(), revokedID); err != nil {
		t.Fatal(err)
	}
	f.get(t, revoked, "/api/v1/users/me", 401)
	close(f.probe.release)
	for range 8 {
		if err := waitTokenUsageWrite(t, f.probe); err != nil {
			t.Fatal(err)
		}
	}
	waitTokenUsage(t, func() bool {
		f.get(t, f.token, "/api/v1/users/me", 200)
		row, err := f.repo.Queries.GetApiTokenByID(t.Context(), f.tokenID)
		return err == nil && row.LastUsedAt.Valid
	})
	if f.probe.peak.Load() > 8 {
		t.Fatal("telemetry exceeded the limit after recovery")
	}
}

func testTokenUsageFailure(t *testing.T) {
	f := newTokenUsageFixture(t, true)
	close(f.probe.release)
	f.get(t, f.token, "/api/v1/users/me", 200)
	if err := waitTokenUsageWrite(t, f.probe); err == nil {
		t.Fatal("injected failure missing")
	}
	waitTokenUsage(
		t,
		func() bool { f.get(t, f.token, "/api/v1/users/me", 200); return f.probe.calls.Load() >= 2 },
	)
}

func testTokenUsageStreamRevocation(t *testing.T) {
	f := newTokenUsageFixture(t, false)
	project := seedProject(t, f.repo)
	env := seedEnv(t, f.repo)
	release := seedRelease(t, f.repo, project.ID)
	deployment := seedDeployment(t, f.repo, release.ID, env.ID, "running")
	if _, err := f.repo.Queries.CreateDeploymentLog(t.Context(), db.CreateDeploymentLogParams{DeploymentID: deployment.ID, Line: "before-revocation"}); err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(
		t.Context(),
		"GET",
		fmt.Sprintf(
			"%s/api/v1/deployments/%d/logs/stream",
			f.srv.URL,
			deployment.ID,
		),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+f.token)
	response, err := f.srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("stream status=%d", response.StatusCode)
	}
	closed := make(chan error, 1)
	go func() { _, err := io.Copy(io.Discard, response.Body); closed <- err }()
	waitTokenUsage(t, func() bool { return f.probe.active.Load() == 1 })
	_, adminToken := seedAPIToken(t, f.repo, f.userID)
	revoke, err := http.NewRequestWithContext(
		t.Context(),
		"DELETE",
		f.srv.URL+"/api/v1/admin/tokens/"+f.tokenID,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	revoke.Header.Set("Authorization", "Bearer "+adminToken)
	result, err := f.srv.Client().Do(revoke)
	if err != nil {
		t.Fatal(err)
	}
	if err := result.Body.Close(); err != nil {
		t.Fatal(err)
	}
	if result.StatusCode != 204 {
		t.Fatalf("revocation status=%d", result.StatusCode)
	}
	f.get(t, f.token, "/api/v1/users/me", 401)
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(1500 * time.Millisecond):
		t.Fatal("revoked stream stayed open while telemetry waited")
	}
}

//go:build e2e

package api_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestConcurrentLogDisconnectAPIWebE2E(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "log-lifecycle@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	session, err := auth.IssueBrowserSession(t.Context(), h.repo,
		auth.BrowserSessionIssue{UserID: user.ID, Audit: func(db.Session) {}})
	if err != nil {
		t.Fatal(err)
	}
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	release := seedRelease(t, h.repo, project.ID)
	deployment := seedDeployment(t, h.repo, release.ID, environment.ID,
		"running")
	if _, err := h.repo.Queries.CreateDeploymentLog(t.Context(),
		db.CreateDeploymentLogParams{DeploymentID: deployment.ID,
			Line: "synthetic-ready"}); err != nil {
		t.Fatal(err)
	}
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	finished := make(chan struct{}, 8)
	srv := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			defer func() { finished <- struct{}{} }()
			router.ServeHTTP(w, r)
		}))
	t.Cleanup(func() { closeStreamServer(t, srv) })
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	streams := openLifecycleStreams(
		t,
		ctx,
		srv,
		deployment.ID,
		token,
		session.ID,
	)
	var workers sync.WaitGroup
	for _, stream := range streams {
		workers.Go(func() { _, _ = io.Copy(io.Discard, stream.Body) })
	}
	workers.Go(func() {
		for range 1000 {
			h.broker.Broadcast(deployment.ID, "synthetic-wakeup")
		}
	})
	// All streams replayed before cancellation races with live wakeups.
	cancel()
	for range streams {
		select {
		case <-finished:
		case <-time.After(3 * time.Second):
			t.Fatal("API or web stream retained a disconnected client")
		}
	}
	workers.Wait()
	h.broker.Broadcast(deployment.ID, "after disconnect")
}

func openLifecycleStreams(
	t *testing.T, ctx context.Context, srv *httptest.Server,
	deploymentID int64, token, session string,
) []*http.Response {
	t.Helper()
	var streams []*http.Response
	for _, prefix := range []string{"/api/v1", ""} {
		for range 4 {
			path := fmt.Sprintf("%s/deployments/%d/logs/stream",
				prefix, deploymentID)
			streams = append(streams,
				openLifecycleStream(t, ctx, srv, path, token, session))
		}
	}
	return streams
}

func openLifecycleStream(
	t *testing.T, ctx context.Context, srv *httptest.Server,
	path, token, session string,
) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, "GET", srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(path, "/api/v1/") {
		req.Header.Set("Authorization", "Bearer "+token)
	} else {
		req.AddCookie(&http.Cookie{Name: "session", Value: session})
	}
	response, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { response.Body.Close() })
	if response.StatusCode != 200 ||
		response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("%s: status=%d", path, response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.Contains(line, "synthetic-ready") {
		t.Fatalf("%s: replay missing: %v", path, err)
	}
	t.Logf("GET %s: status=%d type=%s replay=%q", path,
		response.StatusCode, response.Header.Get("Content-Type"), line)
	return response
}

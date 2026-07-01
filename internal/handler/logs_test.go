package handler

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"github.com/go-chi/chi/v5"
)

func TestStreamLogs_ReplaysHistoricalLogs(t *testing.T) {
	broker := runner.NewLogBroker()
	repo := setupTestRepo(t)
	h := NewLogHandler(broker, repo)

	project, err := repo.Queries.CreateProject(context.Background(), db.CreateProjectParams{
		Name:        "test-project",
		Description: sql.NullString{},
	})
	if err != nil {
		t.Fatal(err)
	}

	env, err := repo.Queries.CreateEnvironment(context.Background(), db.CreateEnvironmentParams{
		Name:        "test-env",
		Description: sql.NullString{},
		Tags:        sql.NullString{},
	})
	if err != nil {
		t.Fatal(err)
	}

	release, err := repo.Queries.CreateRelease(context.Background(), db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "1.0.0",
		StepsJson: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}

	deployment, err := repo.Queries.CreateDeployment(context.Background(), db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: env.ID,
		Status:        "running",
		StartedAt:     sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		FinishedAt:    sql.NullInt64{},
		Forced:        0,
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.Queries.CreateDeploymentLog(context.Background(), db.CreateDeploymentLogParams{
		DeploymentID: deployment.ID,
		StepName:     sql.NullString{String: "Step1", Valid: true},
		Line:         "historical log 1",
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.Queries.CreateDeploymentLog(context.Background(), db.CreateDeploymentLogParams{
		DeploymentID: deployment.ID,
		StepName:     sql.NullString{String: "Step1", Valid: true},
		Line:         "historical log 2",
	})
	if err != nil {
		t.Fatal(err)
	}

	r := chi.NewRouter()
	r.Get("/deployments/{id}/logs/stream", h.StreamLogs)
	srv := httptest.NewServer(r)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/deployments/%d/logs/stream", srv.URL, deployment.ID), nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			lines = append(lines, line)
		}
		if len(lines) >= 2 {
			break
		}
	}

	if len(lines) < 2 {
		t.Fatalf("expected at least 2 historical logs, got %d: %v", len(lines), lines)
	}

	if lines[0] != "data: historical log 1" {
		t.Errorf("expected 'data: historical log 1', got %q", lines[0])
	}
	if lines[1] != "data: historical log 2" {
		t.Errorf("expected 'data: historical log 2', got %q", lines[1])
	}

	go func() {
		time.Sleep(50 * time.Millisecond)
		broker.Broadcast(deployment.ID, "live log")
	}()

	for scanner.Scan() {
		line := scanner.Text()
		if line == "data: live log" {
			return
		}
	}

	t.Fatal("did not receive live log after historical logs")
}

// setupTestRepo opens a temp-file SQLite, runs the real migrations via the
// goose migration runner, and returns a Repository pointing at it. Using
// migrate.Run keeps the schema in lockstep with the rest of the app so adding
// a column to migrations/003_*.sql doesn't require editing every test.
func setupTestRepo(t *testing.T) *repository.Repository {
	t.Helper()
	dir := t.TempDir()
	dsn := fmt.Sprintf("file:%s/test.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", dir)
	sqlDB, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	return repository.New(sqlDB)
}

func TestStreamLogs_BadID(t *testing.T) {
	broker := runner.NewLogBroker()
	repo := setupTestRepo(t)
	h := NewLogHandler(broker, repo)

	req := httptest.NewRequest(http.MethodGet, "/deployments/abc/logs/stream", nil)
	rr := httptest.NewRecorder()
	h.StreamLogs(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

func TestStreamLogs_RealServer(t *testing.T) {
	broker := runner.NewLogBroker()
	repo := setupTestRepo(t)
	h := NewLogHandler(broker, repo)

	r := chi.NewRouter()
	r.Get("/deployments/{id}/logs/stream", h.StreamLogs)
	srv := httptest.NewServer(r)
	defer srv.Close()

	go func() {
		time.Sleep(50 * time.Millisecond)
		broker.Broadcast(1, "real server log")
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf("%s/deployments/1/logs/stream", srv.URL), nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("expected no-cache, got %s", cc)
	}

	scanner := bufio.NewScanner(resp.Body)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
		if len(lines) >= 2 {
			break
		}
	}

	if len(lines) < 2 {
		t.Fatalf("expected at least 2 lines, got %d: %v", len(lines), lines)
	}
	if lines[0] != "data: real server log" {
		t.Errorf("expected 'data: real server log', got %q", lines[0])
	}
	if lines[1] != "" {
		t.Errorf("expected empty line after data, got %q", lines[1])
	}
}

func TestStreamLogs_ClientDisconnect(t *testing.T) {
	broker := runner.NewLogBroker()
	repo := setupTestRepo(t)
	h := NewLogHandler(broker, repo)

	req := httptest.NewRequest(http.MethodGet, "/deployments/1/logs/stream", nil)
	rr := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(req.Context())
	req = req.WithContext(ctx)

	done := make(chan struct{})
	go func() {
		h.StreamLogs(rr, req)
		close(done)
	}()

	time.Sleep(50 * time.Millisecond)

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("handler did not return after context cancel")
	}

	broker.Broadcast(1, "after disconnect")
}

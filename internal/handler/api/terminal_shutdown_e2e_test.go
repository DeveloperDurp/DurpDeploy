//go:build e2e

package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

type terminalRetrySignal chan struct{}

func (signal terminalRetrySignal) Write(data []byte) (int, error) {
	var record struct {
		Message string `json:"msg"`
	}
	if json.Unmarshal(data, &record) == nil &&
		record.Message == "persist deployment terminal status" {
		select {
		case signal <- struct{}{}:
		default:
		}
	}
	return len(data), nil
}

func TestTerminalRetryShutdownAPIWebE2E(t *testing.T) {
	for _, status := range []string{"succeeded", "cleanup_unconfirmed"} {
		t.Run(status, func(t *testing.T) {
			binary := fakePodman(t)
			if status == "cleanup_unconfirmed" {
				cli, err := os.ReadFile(binary)
				if err != nil {
					t.Fatal(err)
				}
				cli = []byte(strings.Replace(string(cli), `case "$3" in`,
					`case "$3" in
volume) if [ "$4" = rm ]; then exit 7; fi;;`, 1))
				if err := os.WriteFile(binary, cli, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			f := newArtifactE2E(t, binary)
			signal := make(terminalRetrySignal, 1)
			previous := slog.Default()
			slog.SetDefault(slog.New(slog.NewJSONHandler(
				io.MultiWriter(os.Stderr, signal), nil)))
			defer slog.SetDefault(previous)
			deployment := startTerminalRetryDeployment(t, f)
			select {
			case <-signal:
			case <-time.After(10 * time.Second):
				t.Fatal("terminal write failure did not admit a retry")
			}
			done := make(chan struct{})
			go func() {
				f.h.runner.KillAll()
				close(done)
			}()
			select {
			case <-done:
				t.Error("shutdown returned before terminal persistence")
			case <-time.After(50 * time.Millisecond):
			}
			if _, err := f.h.repo.DB.ExecContext(t.Context(),
				"DROP TRIGGER reject_api_terminal"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("shutdown did not drain the retry")
			}
			path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
			assertTerminalRetryStatus(t, f, path, status)
			if status == "cleanup_unconfirmed" {
				f.api(t, "POST", path+"/redeploy", map[string]any{}, 409)
			}
			restartTerminalRetryAPI(t, f, binary)
			if status == "cleanup_unconfirmed" {
				// Existing startup reconciliation confirms the empty runtime.
				stored := assertTerminalRetryStatus(t, f, path, "failed")
				if stored.ContainerNamespace.Valid {
					t.Fatalf(
						"startup did not confirm local cleanup: %+v",
						stored,
					)
				}
			} else {
				assertTerminalRetryStatus(t, f, path, status)
			}
		})
	}
}

func startTerminalRetryDeployment(t *testing.T, f *artifactE2E) db.Deployment {
	t.Helper()
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Terminal retry", "script_body": "echo terminal-retry-ok",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "shutdown-retry"}, 201), &release)
	if _, err := f.h.repo.DB.ExecContext(t.Context(), `
CREATE TRIGGER reject_api_terminal BEFORE UPDATE OF status ON deployments
WHEN NEW.status IN ('succeeded','failed','cancelled','cleanup_unconfirmed')
BEGIN SELECT RAISE(FAIL, 'terminal write unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	var deployment db.Deployment
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/deployments",
		map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 201), &deployment)
	return deployment
}

func assertTerminalRetryStatus(
	t *testing.T, f *artifactE2E, path, status string,
) db.Deployment {
	t.Helper()
	var stored db.Deployment
	decodeStepLogTest(t, f.api(t, "GET", path, nil, 200), &stored)
	if stored.Status != status || !stored.FinishedAt.Valid {
		t.Fatalf("terminal API state: %+v", stored)
	}
	page := f.web(t, "GET",
		fmt.Sprintf("/deployments/%d/status", stored.ID), nil, 200)
	if !strings.Contains(page, `data-deployment-status="`+status+`"`) {
		t.Fatalf("web terminal state differs from API: %s", page)
	}
	return stored
}

func restartTerminalRetryAPI(t *testing.T, f *artifactE2E, binary string) {
	t.Helper()
	var filename string
	if err := f.h.repo.DB.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&filename); err != nil {
		t.Fatal(err)
	}
	f.server.Close()
	if err := f.h.repo.DB.Close(); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open("sqlite", filename+
		"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	repo := repository.New(conn)
	rnr := runner.NewWithContainerBinaryForTest(
		repo,
		runner.NewLogBroker(),
		binary,
	)
	t.Cleanup(rnr.KillAll)
	f.h.repo, f.h.runner = repo, rnr
	f.server = httptest.NewServer(server.NewRouter(repo, rnr,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(repo)))
	t.Cleanup(f.server.Close)
	f.baseURL = f.server.URL
}

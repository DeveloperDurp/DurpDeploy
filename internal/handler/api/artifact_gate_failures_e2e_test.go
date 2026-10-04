//go:build e2e

package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

type blockedGateNotifier struct{ release chan struct{} }

func (n blockedGateNotifier) Name() string { return "blocked-gate" }

func (n blockedGateNotifier) Notify(
	ctx context.Context,
	event events.Event,
) (bool, error) {
	if event.Type == events.ArtifactAwaitingApproval {
		select {
		case <-n.release:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	return true, nil
}

func TestArtifactGateDelayedNotificationE2E(t *testing.T) {
	// Given: the previous run remains inside a synchronous gate notification.
	release := make(chan struct{})
	f, deployment, gate := newGateDeployment(t, blockedGateNotifier{release})
	defer close(release)
	// When: approval starts a new continuation before it returns.
	f.api(
		t,
		"POST",
		gateAPIPath(deployment.ID)+"/0/approve",
		map[string]any{"revision": gate.Revision, "sha256": gate.SHA256},
		200,
	)
	// Then: the continuation applies its own read-only approved context.
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
}

func TestArtifactGateRestartConcurrentApprovalE2E(t *testing.T) {
	// Given: a persisted gate, with server, runner and DB connection restarted.
	f, deployment, gate := newGateDeployment(t)
	var filename string
	if err := f.h.repo.DB.QueryRow("SELECT file FROM pragma_database_list WHERE name = 'main'").
		Scan(&filename); err != nil {
		t.Fatal(err)
	}
	f.server.Close()
	f.h.runner.KillAll()
	if err := f.h.repo.DB.Close(); err != nil {
		t.Fatal(err)
	}
	conn, err := sql.Open(
		"sqlite",
		filename+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
	})
	repo := repository.New(conn)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	repo.SetSecretBox(box)
	rnr := runner.New(repo, runner.NewLogBroker())
	t.Cleanup(rnr.KillAll)
	bus := events.NewBus(repo)
	bus.Register(artifactNotifier{f.done})
	rnr.SetEventBus(bus)
	f.h.repo, f.h.runner = repo, rnr
	f.server = httptest.NewServer(
		server.NewRouter(
			repo,
			rnr,
			cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
			handler.NewAuthHandler(repo),
		),
	)
	t.Cleanup(f.server.Close)
	f.baseURL = f.server.URL
	path := gateAPIPath(deployment.ID)
	f.api(t, "GET", path, nil, 200)
	// When: concurrent requests approve the same stored identity.
	data, err := json.Marshal(
		map[string]any{"sha256": gate.SHA256, "revision": gate.Revision},
	)
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(chan int, 4)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			request, err := http.NewRequestWithContext(
				context.Background(),
				"POST",
				f.baseURL+path+"/0/approve",
				bytes.NewReader(data),
			)
			if err != nil {
				statuses <- 0
				return
			}
			request.Header.Set("Authorization", "Bearer "+f.token)
			request.Header.Set("Content-Type", "application/json")
			response, err := f.client.Do(request)
			if err != nil {
				statuses <- 0
				return
			}
			defer response.Body.Close()
			statuses <- response.StatusCode
		})
	}
	group.Wait()
	close(statuses)
	// Then: exactly one request starts continuation using the preserved bytes.
	accepted := 0
	for status := range statuses {
		if status == 200 {
			accepted++
		} else if status != 409 {
			t.Fatalf("status=%d", status)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted=%d", accepted)
	}
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
}

func TestArtifactGateBlocksInvalidArtifactsE2E(t *testing.T) {
	for _, change := range []string{"missing", "tampered", "expired", "wrong-checksum"} {
		t.Run(change, func(t *testing.T) {
			// Given: a gate whose approval identity or retained bytes are invalid.
			f, deployment, gate := newGateDeployment(t)
			var err error
			switch change {
			case "missing":
				_, err = f.h.repo.DB.Exec(
					"DELETE FROM artifact_gate_chunks WHERE deployment_id = ?",
					deployment.ID,
				)
			case "tampered":
				_, err = f.h.repo.DB.Exec(
					"UPDATE artifact_gate_chunks SET ciphertext = 'invalid' WHERE deployment_id = ?",
					deployment.ID,
				)
			case "expired":
				_, err = f.h.repo.DB.Exec(
					"UPDATE artifact_gates SET expires_at = 0 WHERE deployment_id = ?",
					deployment.ID,
				)
			case "wrong-checksum":
				gate.SHA256 = "changed"
			}
			if err != nil {
				t.Fatal(err)
			}
			// When: approval is attempted through the API.
			f.api(
				t,
				"POST",
				gateAPIPath(deployment.ID)+"/0/approve",
				map[string]any{
					"sha256":   gate.SHA256,
					"revision": gate.Revision,
				},
				409,
			)
			// Then: continuation is not running or successful.
			var state db.Deployment
			if err := json.Unmarshal(
				f.api(
					t,
					"GET",
					fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
					nil,
					200,
				),
				&state,
			); err != nil {
				t.Fatal(err)
			}
			if state.Status == "running" || state.Status == "succeeded" {
				t.Fatalf("state=%s", state.Status)
			}
		})
	}
}

func TestArtifactGateCancellationAndAccessE2E(t *testing.T) {
	// Given: a pending gate and unauthorized users.
	f, deployment, gate := newGateDeployment(t)
	adminToken := f.token
	for _, role := range []string{"viewer", "deployer"} {
		user := seedAPIUser(t, f.h.repo, role+"@gate.example", role)
		if role == "viewer" {
			if err := f.h.repo.Queries.AddProjectMember(
				t.Context(),
				db.AddProjectMemberParams{
					ProjectID: f.project.ID,
					UserID:    user.ID,
					Role:      "deployer",
				},
			); err != nil {
				t.Fatal(err)
			}
		}
		_, f.token = seedAPIToken(t, f.h.repo, user.ID)
		f.api(t, "GET", gateAPIPath(deployment.ID)+"/0/artifact", nil, 403)
		f.api(
			t,
			"POST",
			gateAPIPath(deployment.ID)+"/0/approve",
			map[string]any{"sha256": gate.SHA256, "revision": gate.Revision},
			403,
		)
	}
	f.token = adminToken
	// When: the authorized operator cancels pending work through the API.
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/cancel", deployment.ID),
		nil,
		200,
	)
	// Then: cancellation is terminal and approval cannot start apply.
	f.api(
		t,
		"POST",
		gateAPIPath(deployment.ID)+"/0/approve",
		map[string]any{"sha256": gate.SHA256, "revision": gate.Revision},
		409,
	)
}

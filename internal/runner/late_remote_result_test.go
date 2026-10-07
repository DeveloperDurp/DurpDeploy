package runner_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
)

func TestLiveRunnerLateSuccessPreservesMaintenanceFailure(t *testing.T) {
	// Given a live runner waiting for its final remote step.
	repo, rnr, notifications := setupRunnerHarness(t)
	repo.DB.SetMaxOpenConns(1)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "late-result"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "late-result"},
	)
	if err != nil {
		t.Fatal(err)
	}
	seedAgentWithInterpreter(t, repo, environment.ID, "a", "bash")
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1",
			StepsJson: `[{"name":"remote","script_body":"echo ok","execution_target":"agent","agent_selectors":["linux"]}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		rnr.Run(t.Context(), created.Deployment.ID, release.ID, environment.ID)
	}()
	select {
	case <-repo.RemoteWorkReady():
	case <-time.After(3 * time.Second):
		t.Fatal("remote work was not queued")
	}

	// When maintenance and the late report finish before the next runner read.
	// One transaction makes this ordering deterministic without timing sleeps.
	hash := sha256.Sum256([]byte("late-claim"))
	err = repo.WithDeploymentTx(t.Context(), created.Deployment.ID,
		func(ctx context.Context, q *db.Queries) error {
			now := sql.NullInt64{Int64: 100, Valid: true}
			rows, err := q.ClaimRemoteStepRun(ctx, db.ClaimRemoteStepRunParams{
				DeploymentID: created.Deployment.ID, AgentID: "a", ClaimTokenHash: hash[:],
				Ciphertext: sql.NullString{String: "payload", Valid: true},
				Now:        now, ClaimExpiresAt: sql.NullInt64{Int64: 200, Valid: true},
			})
			if err != nil || rows != 1 {
				return fmt.Errorf("claim rows=%d error=%v", rows, err)
			}
			rows, err = q.StartRemoteStepRun(ctx, db.StartRemoteStepRunParams{
				DeploymentID: created.Deployment.ID, AgentID: "a", ClaimTokenHash: hash[:], Now: now,
			})
			if err != nil || rows != 1 {
				return fmt.Errorf("start rows=%d error=%v", rows, err)
			}
			rows, err = q.LoseStaleRemoteStepRuns(
				ctx,
				db.LoseStaleRemoteStepRunsParams{
					Now: now, StaleBefore: now,
				},
			)
			if err != nil || rows != 1 {
				return fmt.Errorf("lose rows=%d error=%v", rows, err)
			}
			rows, err = q.FailDeploymentsWithTerminalRemoteStepRuns(ctx, now)
			if err != nil || rows != 1 {
				return fmt.Errorf("maintenance rows=%d error=%v", rows, err)
			}
			rows, err = q.FinishRemoteStepRun(ctx, db.FinishRemoteStepRunParams{
				DeploymentID: created.Deployment.ID, AgentID: "a", ClaimTokenHash: hash[:],
				Now: now, State: "succeeded",
			})
			if err != nil || rows != 1 {
				return fmt.Errorf("late outcome rows=%d error=%v", rows, err)
			}
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("live runner did not finish")
	}
	// Then the durable success is acknowledged but cannot revive the deployment.
	handled, err := repo.FinishRemoteStep(
		t.Context(),
		repository.RemoteLifecycleClaim{
			DeploymentID: created.Deployment.ID, AgentID: "a", ClaimTokenHash: hash[:],
		},
		"succeeded",
	)
	if err != nil || !handled {
		t.Fatalf("durable success handled=%v error=%v", handled, err)
	}
	stored, err := repo.Queries.GetDeployment(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil || stored.Status != "failed" {
		t.Fatalf("late success revived deployment=%+v error=%v", stored, err)
	}
	for _, event := range notifications.events {
		if event.Type == events.DeploymentSucceeded {
			t.Fatal("late result emitted a successful deployment event")
		}
	}
}

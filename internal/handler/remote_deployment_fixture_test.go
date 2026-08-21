package handler_test

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

const protocolSecretSentinel = "claim-token-must-not-be-rendered"

func seedRemoteDeployment(
	t *testing.T,
	harness *testHarness,
	dispatchState string,
	deploymentStatus string,
) db.Deployment {
	t.Helper()
	ctx := context.Background()
	project, err := harness.repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name: fmt.Sprintf("remote-project-%s", dispatchState),
		},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := harness.repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{
			Name: fmt.Sprintf("remote-environment-%s", dispatchState),
		},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := harness.repo.Queries.CreateRelease(
		ctx,
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "1.0.0",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := harness.repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        deploymentStatus,
		},
	)
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	agentID := "agent-" + dispatchState
	_, err = harness.repo.DB.ExecContext(ctx, `
		INSERT INTO agents (
			id, name, status, certificate_pem, certificate_fingerprint, last_heartbeat_at
		) VALUES (?, 'Remote agent', 'active', 'certificate', ?, 100)
	`, agentID, strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	_, err = harness.repo.DB.ExecContext(
		ctx,
		"INSERT INTO environment_agent_assignments (environment_id, agent_id) VALUES (?, ?)",
		environment.ID, agentID,
	)
	if err != nil {
		t.Fatalf("assign environment agent: %v", err)
	}
	_, err = harness.repo.DB.ExecContext(ctx, `
		INSERT INTO deployment_dispatches (
			deployment_id, mode, assigned_agent_id, state, reason, claim_token_hash
		) VALUES (?, 'remote', ?, ?, ?, ?)
	`, deployment.ID, agentID, dispatchState, protocolSecretSentinel,
		[]byte(protocolSecretSentinel))
	if err != nil {
		t.Fatalf("create direct dispatch: %v", err)
	}
	return deployment
}

func seedLocalDeployment(t *testing.T, harness *testHarness) {
	t.Helper()
	ctx := context.Background()
	project, err := harness.repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name: "local-project",
		},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := harness.repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "local-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := harness.repo.Queries.CreateRelease(
		ctx,
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "1.0.0",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	_, err = harness.repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: environment.ID,
			Status:        "failed",
			Note:          sql.NullString{},
		},
	)
	if err != nil {
		t.Fatalf("create local deployment: %v", err)
	}
}

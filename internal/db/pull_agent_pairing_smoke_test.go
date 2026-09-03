package db_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

func TestPullAgentPairingQueriesSmoke(t *testing.T) {
	ctx := context.Background()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	queries := db.New(conn)

	project, err := queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "pairing-project"},
	)
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "pairing-environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	release, err := queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "pairing-v1",
		StepsJson: "[]",
	})
	if err != nil {
		t.Fatalf("create release: %v", err)
	}
	deployment, err := queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
	})
	if err != nil {
		t.Fatalf("create deployment: %v", err)
	}
	agent, err := queries.CreatePendingAgent(ctx, db.CreatePendingAgentParams{
		ID: "paired-agent", Name: "paired-agent",
	})
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	codeHash := bytes.Repeat([]byte{1}, 32)
	if _, err := queries.CreateAgentPairing(ctx, db.CreateAgentPairingParams{
		AgentID:             agent.ID,
		PairingCodeHash:     codeHash,
		AgentPublicIdentity: "agent-public-identity",
		AgentPin:            pairingSmokePin("a"),
		ExpiresAt:           2_000,
	}); err != nil {
		t.Fatalf("create agent pairing: %v", err)
	}
	if _, err := queries.BeginAgentPairing(ctx, db.BeginAgentPairingParams{
		UpdatedAt: 999, AgentID: agent.ID, PairingCodeHash: codeHash,
		AgentPin: pairingSmokePin("a"), Now: 999,
	}); err != nil {
		t.Fatalf("begin agent pairing: %v", err)
	}
	completeInput := db.CompleteAgentPairingParams{
		AgentPublicIdentity: "agent-public-identity",
		ServerPublicIdentity: sql.NullString{
			String: "server-public-identity",
			Valid:  true,
		},
		ServerPin: sql.NullString{
			String: pairingSmokePin("b"),
			Valid:  true,
		},
		PairedAt:        sql.NullInt64{Int64: 1_000, Valid: true},
		UpdatedAt:       1_000,
		AgentID:         agent.ID,
		PairingCodeHash: codeHash,
		AgentPin:        pairingSmokePin("a"),
	}
	if _, err := queries.CompleteAgentPairing(
		ctx,
		completeInput,
	); err != nil {
		t.Fatalf("complete agent pairing: %v", err)
	}
	activateInput := db.ActivatePairedAgentParams{
		CertificatePem: sql.NullString{
			String: "agent-public-identity",
			Valid:  true,
		},
		CertificateFingerprint: sql.NullString{
			String: pairingSmokePin("a"),
			Valid:  true,
		},
		LastHeartbeatAt: sql.NullInt64{Int64: 1_000, Valid: true},
		EnrolledAt:      sql.NullInt64{Int64: 1_000, Valid: true},
		UpdatedAt:       1_000,
		ID:              agent.ID,
		PairingCodeHash: codeHash,
	}
	if _, err := queries.ActivatePairedAgent(
		ctx,
		activateInput,
	); err != nil {
		t.Fatalf("activate paired agent: %v", err)
	}
	if _, err := queries.CompleteAgentPairing(ctx, completeInput); err != nil {
		t.Fatalf("repeat complete agent pairing: %v", err)
	}
	if _, err := queries.ActivatePairedAgent(ctx, activateInput); err != nil {
		t.Fatalf("repeat activate paired agent: %v", err)
	}
	assignment, err := queries.AssignAgentToEnvironment(
		ctx,
		db.AssignAgentToEnvironmentParams{
			EnvironmentID: environment.ID, AgentID: agent.ID, UpdatedAt: 1_000,
		},
	)
	if err != nil {
		t.Fatalf("assign agent to environment: %v", err)
	}
	dispatch, err := queries.CreateDirectDeploymentDispatch(
		ctx,
		db.CreateDirectDeploymentDispatchParams{
			DeploymentID: deployment.ID,
			AssignedAgentID: sql.NullString{
				String: assignment.AgentID,
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatalf("create direct dispatch: %v", err)
	}
	claimed, err := queries.ClaimOldestDirectDeployment(
		ctx,
		db.ClaimOldestDirectDeploymentParams{
			AgentID:         sql.NullString{String: agent.ID, Valid: true},
			ClaimTokenHash:  bytes.Repeat([]byte{2}, 32),
			ClaimExpiresAt:  sql.NullInt64{Int64: 2_000, Valid: true},
			LastHeartbeatAt: sql.NullInt64{Int64: 1_000, Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("claim direct deployment: %v", err)
	}
	if claimed.DeploymentID != dispatch.DeploymentID ||
		claimed.AssignedAgentID.String != agent.ID || claimed.State != "claimed" {
		t.Fatalf("claimed direct dispatch = %#v", claimed)
	}
}

func pairingSmokePin(character string) string {
	return string(bytes.Repeat([]byte(character), 64))
}

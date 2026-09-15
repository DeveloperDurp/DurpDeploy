package dispatch

import (
	"bytes"
	"database/sql"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func engineIndex(name string) int {
	switch name {
	case "SQLite":
		return 1
	case "PostgreSQL":
		return 2
	default:
		return 3
	}
}

func seedRemoteLifecycle(
	t *testing.T,
	repo *repository.Repository,
	seed int64,
) repository.RemoteLifecycleClaim {
	t.Helper()
	ctx := t.Context()
	project, err := repo.Queries.CreateProject(
		ctx, db.CreateProjectParams{Name: fmt.Sprintf("project-%d", seed)},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		ctx, db.CreateEnvironmentParams{Name: fmt.Sprintf("env-%d", seed)},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	agentID := fmt.Sprintf("agent-%d", seed)
	if _, err := repo.Queries.CreateAgent(ctx, db.CreateAgentParams{
		ID: agentID, Name: agentID, Endpoint: "https://agent.invalid",
	}); err != nil {
		t.Fatal(err)
	}
	code := bytes.Repeat([]byte{byte(seed)}, 32)
	pin := fmt.Sprintf("%064x", seed)
	if _, err := repo.Queries.CreateAgentPairing(
		ctx,
		db.CreateAgentPairingParams{
			AgentID: agentID, PairingCodeHash: code,
			AgentPublicIdentity: "public", AgentPin: pin, ExpiresAt: 500,
		},
	); err != nil {
		t.Fatal(err)
	}
	if changed, err := repo.Queries.BeginPairingCommit(
		ctx,
		db.BeginPairingCommitParams{
			AgentID: agentID, PairingCodeHash: code, Now: 100,
			ServerPublicIdentity: sql.NullString{String: "public", Valid: true},
			ServerPin:            sql.NullString{String: pin, Valid: true},
			EncryptedIdentity:    sql.NullString{String: "cipher", Valid: true},
		},
	); err != nil || changed != 1 {
		t.Fatalf("begin pairing rows=%d error=%v", changed, err)
	}
	if changed, err := repo.CommitAgentPairing(
		ctx,
		db.CompleteAgentPairingParams{
			AgentID: agentID, Now: sql.NullInt64{Int64: 100, Valid: true},
			ServerPin: sql.NullString{String: pin, Valid: true},
		},
		db.ActivatePairedAgentParams{
			CertificatePem: sql.NullString{String: "cert", Valid: true},
			CertificateFingerprint: sql.NullString{
				String: pin, Valid: true,
			},
		},
	); err != nil || changed != 1 {
		t.Fatalf("commit pairing rows=%d error=%v", changed, err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID, Status: "pending",
			AssignedAgentID: sql.NullString{String: agentID, Valid: true},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if changed, err := repo.Queries.CreateRemoteDeploymentClaim(
		ctx, deployment.ID,
	); err != nil || changed != 1 {
		t.Fatalf("create claim rows=%d error=%v", changed, err)
	}
	now, err := repo.Queries.CurrentUnixTime(ctx)
	if err != nil {
		t.Fatal(err)
	}
	hash := bytes.Repeat([]byte{byte(seed + 1)}, 32)
	if changed, err := repo.ClaimRemoteDeployment(
		ctx,
		db.ClaimRemoteDeploymentParams{
			ClaimTokenHash: hash,
			Ciphertext:     sql.NullString{String: "cipher", Valid: true},
			ClaimExpiresAt: now + 60, Now: now,
			DeploymentID: deployment.ID, AgentID: agentID,
		},
	); err != nil || changed != 1 {
		t.Fatalf("claim rows=%d error=%v", changed, err)
	}
	identity := repository.RemoteLifecycleClaim{
		DeploymentID: deployment.ID, AgentID: agentID, ClaimTokenHash: hash,
	}
	if err := repo.StartRemoteDeployment(ctx, identity); err != nil {
		t.Fatal(err)
	}
	return identity
}

func revokeRemoteAgent(
	t *testing.T,
	repo *repository.Repository,
	agentID string,
) {
	t.Helper()
	agent, err := repo.Queries.SetAgentStatus(
		t.Context(), db.SetAgentStatusParams{ID: agentID, Status: "revoked"},
	)
	if err != nil || agent.Status != "revoked" {
		t.Fatalf("revoke agent=%+v error=%v", agent, err)
	}
}

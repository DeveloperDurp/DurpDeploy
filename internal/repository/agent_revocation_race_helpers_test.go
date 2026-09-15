package repository

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func seedRevocationRaceFixture(
	t *testing.T,
	repo *Repository,
	suffix string,
) revocationRaceFixture {
	t.Helper()
	ctx := t.Context()
	agentID := "matrix-" + strings.ToLower(suffix)
	environment, err := repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: agentID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.CreateAgent(ctx, db.CreateAgentParams{
		ID: agentID, Name: agentID, Endpoint: "https://agent.invalid",
	}); err != nil {
		t.Fatal(err)
	}
	codeHash := sha256.Sum256([]byte("pairing-" + agentID))
	code := codeHash[:]
	pinHash := sha256.Sum256([]byte("pin-" + agentID))
	pin := hex.EncodeToString(pinHash[:])
	serverPinHash := sha256.Sum256([]byte("server-pin-" + agentID))
	serverPin := hex.EncodeToString(serverPinHash[:])
	if _, err := repo.Queries.CreateAgentPairing(
		ctx,
		db.CreateAgentPairingParams{
			AgentID: agentID, PairingCodeHash: code,
			AgentPublicIdentity: "public", AgentPin: pin, ExpiresAt: 4102444800,
		},
	); err != nil {
		t.Fatal(err)
	}
	if rows, err := repo.Queries.BeginPairingCommit(
		ctx,
		db.BeginPairingCommitParams{
			AgentID: agentID, PairingCodeHash: code, Now: 100,
			ServerPublicIdentity: raceString(
				"server",
			), ServerPin: raceString(serverPin),
			EncryptedIdentity: raceString("ciphertext"),
		},
	); err != nil ||
		rows != 1 {
		t.Fatalf("pairing commit rows=%d err=%v", rows, err)
	}
	if rows, err := repo.CommitAgentPairing(ctx, db.CompleteAgentPairingParams{
		AgentID: agentID, Now: raceInt(100), ServerPin: raceString(serverPin),
	}, db.ActivatePairedAgentParams{
		CertificatePem:         raceString("public"),
		CertificateFingerprint: raceString(pin),
	}); err != nil || rows != 1 {
		t.Fatalf("pairing activation rows=%d err=%v", rows, err)
	}
	if !strings.HasPrefix(suffix, "Assignment") {
		if err := repo.AssignEnvironmentAgent(
			ctx,
			environment.ID,
			agentID,
		); err != nil {
			t.Fatal(err)
		}
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: environment.ID, Status: "pending",
			AssignedAgentID: raceString(agentID),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := repo.Queries.CreateRemoteDeploymentClaim(
		ctx,
		deployment.ID,
	); err != nil ||
		rows != 1 {
		t.Fatalf("create claim rows=%d err=%v", rows, err)
	}
	tokenHash := sha256.Sum256([]byte("claim-" + agentID))
	return revocationRaceFixture{
		agentID: agentID, environmentID: environment.ID,
		deploymentID: deployment.ID,
		identity: RemoteLifecycleClaim{DeploymentID: deployment.ID,
			AgentID: agentID, ClaimTokenHash: tokenHash[:]},
	}
}

func raceClaimParams(
	t *testing.T,
	repo *Repository,
	fixture revocationRaceFixture,
) db.ClaimRemoteDeploymentParams {
	t.Helper()
	return raceClaimParamsContext(t.Context(), repo, fixture)
}

func raceClaimParamsContext(
	ctx context.Context,
	repo *Repository,
	fixture revocationRaceFixture,
) db.ClaimRemoteDeploymentParams {
	return raceClaimParamsQueries(ctx, repo.Queries, fixture)
}

func raceClaimParamsQueries(
	ctx context.Context,
	q *db.Queries,
	fixture revocationRaceFixture,
) db.ClaimRemoteDeploymentParams {
	now, _ := q.CurrentUnixTime(ctx)
	return db.ClaimRemoteDeploymentParams{
		DeploymentID: fixture.deploymentID, AgentID: fixture.agentID,
		ClaimTokenHash: fixture.identity.ClaimTokenHash,
		Ciphertext: raceString(
			"ciphertext",
		), Now: now, ClaimExpiresAt: now + 60,
	}
}

func runRevocationFirst(
	t *testing.T,
	operationRepo *Repository,
	revocationRepo *Repository,
	operation revocationOperation,
	fixture revocationRaceFixture,
) (bool, error) {
	t.Helper()
	ctx := t.Context()
	tx, err := revocationRepo.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if changed, err := revokeAgent(
		ctx, revocationRepo.Queries.WithTx(tx), fixture.agentID,
	); err != nil || !changed {
		t.Fatalf("revoke changed=%v err=%v", changed, err)
	}
	ready := make(chan struct{})
	done := make(chan raceOperationResult, 1)
	go func() {
		close(ready)
		wrote, err := operation.run(ctx, operationRepo, fixture)
		done <- raceOperationResult{wrote: wrote, err: err}
	}()
	<-ready
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	result := <-done
	return result.wrote, result.err
}

type raceOperationResult struct {
	wrote bool
	err   error
}

func runOperationFirst(
	t *testing.T,
	operationRepo *Repository,
	revocationRepo *Repository,
	operation revocationOperation,
	fixture revocationRaceFixture,
) (bool, error) {
	t.Helper()
	ctx := t.Context()
	tx, err := operationRepo.DB.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	wrote, err := operation.runTx(
		ctx, operationRepo.Queries.WithTx(tx), fixture,
	)
	if err != nil || !wrote {
		return wrote, err
	}
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(ready)
		_, err := revocationRepo.RevokeAgent(ctx, fixture.agentID)
		done <- err
	}()
	<-ready
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	return wrote, nil
}

func raceString(value string) sql.NullString {
	return sql.NullString{String: value, Valid: true}
}

func raceInt(value int64) sql.NullInt64 {
	return sql.NullInt64{Int64: value, Valid: true}
}

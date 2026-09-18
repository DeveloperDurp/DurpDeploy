package repository_test

import (
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRevocationBeforeClaimFailsWork(t *testing.T) {
	repo := remoteFixture(t)
	changed, err := repo.RevokeAgent(t.Context(), "a")
	if err != nil || !changed {
		t.Fatalf("revoke changed=%v err=%v", changed, err)
	}
	rows, err := repo.ClaimRemoteDeployment(t.Context(), claimArg("a"))
	if err != nil || rows != 0 {
		t.Fatalf("claim rows=%d err=%v", rows, err)
	}
	assertRevokedUnstarted(t, repo)
}

func TestRevocationAfterClaimFailsWork(t *testing.T) {
	repo := remoteFixture(t)
	claim := currentClaimArg(t, repo)
	rows, err := repo.ClaimRemoteDeployment(t.Context(), claim)
	assertOne(t, rows, err)
	if _, err := repo.RevokeAgent(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	assertRevokedUnstarted(t, repo)
	if err := repo.StartRemoteDeployment(
		t.Context(),
		remoteIdentity(claim),
	); !errors.Is(
		err,
		repository.ErrRemoteLifecycleConflict,
	) {
		t.Fatalf("start after revoke err=%v", err)
	}
}

func TestRevocationAfterStartMarksLost(t *testing.T) {
	repo := remoteFixture(t)
	claim := currentClaimArg(t, repo)
	rows, err := repo.ClaimRemoteDeployment(t.Context(), claim)
	assertOne(t, rows, err)
	if err := repo.StartRemoteDeployment(
		t.Context(),
		remoteIdentity(claim),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RevokeAgent(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	persistedClaim, err := repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || persistedClaim.State != "lost" ||
		persistedClaim.Reason.String != "remote_agent_revoked_after_start" {
		t.Fatalf("claim=%+v err=%v", persistedClaim, err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "failed" {
		t.Fatalf("deployment=%+v err=%v", deployment, err)
	}
}

func remoteIdentity(
	claim db.ClaimRemoteDeploymentParams,
) repository.RemoteLifecycleClaim {
	return repository.RemoteLifecycleClaim{
		DeploymentID:   claim.DeploymentID,
		AgentID:        claim.AgentID,
		ClaimTokenHash: claim.ClaimTokenHash,
	}
}

func currentClaimArg(
	t *testing.T,
	repo *repository.Repository,
) db.ClaimRemoteDeploymentParams {
	t.Helper()
	now, err := repo.Queries.CurrentUnixTime(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	claim := claimArg("a")
	claim.Now = now
	claim.ClaimExpiresAt = now + 60
	return claim
}

func assertRevokedUnstarted(
	t *testing.T,
	repo *repository.Repository,
) {
	t.Helper()
	claim, err := repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claim.State != "failed" ||
		claim.Reason.String != "remote_agent_revoked_before_start" {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "failed" {
		t.Fatalf("deployment=%+v err=%v", deployment, err)
	}
}

package repository_test

import (
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/logscrub"
	"durpdeploy/internal/repository"
)

func TestRevokePendingAgentWithCommittingPairing(t *testing.T) {
	repo := remoteFixture(t)
	if _, err := repo.DB.Exec(
		`UPDATE agents SET status = 'pending' WHERE id = 'a'`,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.Exec(`UPDATE agent_pairings
		SET state = 'committing', paired_at = NULL WHERE agent_id = 'a'`); err != nil {
		t.Fatal(err)
	}

	changed, err := repo.RevokeAgent(t.Context(), "a")
	if err != nil || !changed {
		t.Fatalf("revoke changed=%v error=%v", changed, err)
	}
	agent, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil || agent.Status != "revoked" {
		t.Fatalf("agent status=%q error=%v", agent.Status, err)
	}
}

func TestRemoteTerminalFlushSurvivesAgentRevocation(t *testing.T) {
	repo := remoteFixture(t)
	claim := currentClaimArg(t, repo)
	rows, err := repo.ClaimRemoteDeployment(t.Context(), claim)
	assertOne(t, rows, err)
	identity := remoteIdentity(claim)
	if err := repo.StartRemoteDeployment(t.Context(), identity); err != nil {
		t.Fatal(err)
	}
	scrubber := logscrub.New([]string{"top-secret"})
	logs, err := repo.AppendRemoteDeploymentLogs(
		t.Context(), identity,
		[]repository.RemoteLogEvent{{Sequence: 1, Line: "top-"}},
		scrubber,
	)
	if err != nil || len(logs) != 0 {
		t.Fatalf("buffered logs=%+v error=%v", logs, err)
	}
	result, err := repo.FinishRemoteDeploymentLifecycle(
		t.Context(), identity, "succeeded",
	)
	if err != nil || !result.Changed {
		t.Fatalf("finish result=%+v error=%v", result, err)
	}
	if changed, err := repo.RevokeAgent(
		t.Context(),
		"a",
	); err != nil ||
		!changed {
		t.Fatalf("revoke changed=%v error=%v", changed, err)
	}
	logs, err = repo.FlushRemoteDeploymentLogs(
		t.Context(), identity, scrubber,
	)
	if err != nil || len(logs) != 1 || logs[0].Line != "top-" {
		t.Fatalf("flushed logs=%+v error=%v", logs, err)
	}
	claimRow, err := repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claimRow.LogBufferCiphertext.Valid {
		t.Fatalf("terminal claim=%+v error=%v", claimRow, err)
	}
}

func TestRevocationClearsRemoteStepBuffers(t *testing.T) {
	repo := remoteFixture(t)
	if _, err := repo.DB.Exec(`UPDATE deployments SET status='running'
		WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.Exec(`INSERT INTO remote_step_runs
		(deployment_id,step_index,agent_id,state,claim_token_hash,ciphertext,
		 claim_expires_at,last_heartbeat_at,started_at,updated_at,
		 log_buffer_ciphertext)
		VALUES (3,0,'a','started',zeroblob(32),'ciphertext',200,100,100,100,
		 zeroblob(16))`); err != nil {
		t.Fatal(err)
	}
	if changed, err := repo.RevokeAgent(
		t.Context(),
		"a",
	); err != nil ||
		!changed {
		t.Fatalf("revoke changed=%v error=%v", changed, err)
	}
	var state string
	var logBuffer []byte
	if err := repo.DB.QueryRow(`SELECT state,log_buffer_ciphertext
		FROM remote_step_runs WHERE deployment_id=3 AND step_index=0
		AND agent_id='a'`).Scan(&state, &logBuffer); err != nil {
		t.Fatal(err)
	}
	if state != "lost" || logBuffer != nil {
		t.Fatalf("remote step state=%q log_buffer=%x", state, logBuffer)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 3)
	if err != nil || deployment.Status != "failed" {
		t.Fatalf("deployment=%+v error=%v", deployment, err)
	}
}

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
	if _, err := repo.DB.Exec(`UPDATE remote_deployment_claims
		SET log_buffer_ciphertext=zeroblob(16) WHERE deployment_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.RevokeAgent(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	persistedClaim, err := repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || persistedClaim.State != "lost" ||
		persistedClaim.Reason.String != "remote_agent_revoked_after_start" ||
		persistedClaim.LogBufferCiphertext.Valid {
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

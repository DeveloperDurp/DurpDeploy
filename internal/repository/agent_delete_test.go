package repository_test

import (
	"bytes"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestAgentDeleteRetainsHistoryAndRevokesAccess(t *testing.T) {
	// Given: an agent referenced by queued deployments and their snapshots.
	repo := remoteFixture(t)
	if _, err := repo.Queries.CreateDeploymentLog(t.Context(),
		db.CreateDeploymentLogParams{
			DeploymentID: 1, StepName: ns("remote"), Line: "retained output",
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.Exec(`UPDATE deployment_step_attempts
		SET state='succeeded',agent_id='a',claim_token_hash=zeroblob(32),
		claim_expires_at=200,started_at=100,finished_at=101
		WHERE deployment_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.Exec(`INSERT INTO deployment_dispatches
		(deployment_id,step_index,attempt,ciphertext)
		VALUES(1,0,1,'agent-payload')`); err != nil {
		t.Fatal(err)
	}

	// When
	if err := repo.DeleteAgent(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}

	// Then: inventory lookup is gone, history remains, and claims cannot start.
	if _, err := repo.Queries.GetAgent(t.Context(), "a"); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("deleted lookup error=%v", err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.AssignedAgentID.Valid ||
		deployment.Status != "failed" {
		t.Fatalf("history=%+v error=%v", deployment, err)
	}
	assertAgentDeletionRecordsRemoved(t, repo)
	var state, line string
	var agent, token, expiry, payload any
	if err := repo.DB.QueryRow(`SELECT a.state,a.agent_id,a.claim_token_hash,
		a.claim_expires_at,d.ciphertext FROM deployment_step_attempts a
		JOIN deployment_dispatches d USING(deployment_id,step_index,attempt)
		WHERE a.deployment_id=1`).Scan(&state, &agent, &token, &expiry, &payload); err != nil || state != "succeeded" || agent != nil || token != nil ||
		expiry != nil || payload != nil {
		t.Fatalf(
			"retained attempt state=%s agent=%v token=%v expiry=%v payload=%v error=%v",
			state,
			agent,
			token,
			expiry,
			payload,
			err,
		)
	}
	if err := repo.DB.QueryRow(`SELECT line FROM deployment_logs
		WHERE deployment_id=1`).Scan(&line); err != nil || line != "retained output" {
		t.Fatalf("retained output=%q error=%v", line, err)
	}
	rows, err := repo.ClaimRemoteDeployment(t.Context(), claimArg("a"))
	if err != nil || rows != 0 {
		t.Fatalf("claim rows=%d error=%v", rows, err)
	}
	rows, err = repo.Queries.HeartbeatAgent(
		t.Context(),
		db.HeartbeatAgentParams{
			ID:                     "a",
			Now:                    ni(200),
			CertificateFingerprint: ns(strings.Repeat("a", 64)),
		},
	)
	if err != nil || rows != 0 {
		t.Fatalf("heartbeat rows=%d error=%v", rows, err)
	}
}

func TestAgentDeleteBlocksTerminalRunInActiveFanout(t *testing.T) {
	// Given: this agent completed, but another target still controls the result.
	repo := remoteFixture(t)
	if _, err := repo.DB.Exec(`UPDATE deployments SET status='running',
		assigned_agent_id=NULL WHERE id=3`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.Exec(`INSERT INTO remote_step_runs
		(deployment_id,step_index,agent_id,state,claim_token_hash,ciphertext,
		claim_expires_at,last_heartbeat_at,started_at,finished_at)
		VALUES(3,0,'a','failed',zeroblob(32),'payload',200,100,100,101),
		(3,0,'b','started',randomblob(32),'payload',200,100,100,NULL)`); err != nil {
		t.Fatal(err)
	}

	// When
	err := repo.DeleteAgent(t.Context(), "a")

	// Then: deleting a completed target cannot erase the fanout failure.
	if !errors.Is(err, repository.ErrAgentDeletionBlocked) {
		t.Fatalf("delete error=%v", err)
	}
	agent, err := repo.Queries.GetAgent(t.Context(), "a")
	if err != nil || agent.Status != "active" {
		t.Fatalf("agent=%+v error=%v", agent, err)
	}
	var count int
	if err := repo.DB.QueryRow(`SELECT COUNT(*) FROM remote_step_runs
		WHERE deployment_id=3`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("fanout rows=%d error=%v", count, err)
	}
}

func TestAgentDeleteAllowsConfirmedCleanup(t *testing.T) {
	// Given: completed cleanup and a terminal deployment.
	repo := remoteFixture(t)
	if _, err := repo.DB.Exec(`UPDATE remote_deployment_claims
		SET state='cleanup_unconfirmed',claim_token_hash=zeroblob(32),
		ciphertext='payload',claim_expires_at=200,last_heartbeat_at=100,
		started_at=100,finished_at=101,cleanup_confirmed_at=200
		WHERE deployment_id=1`); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.Exec(`UPDATE deployments SET status='cleanup_unconfirmed',
		finished_at=200 WHERE id=1`); err != nil {
		t.Fatal(err)
	}

	// When
	err := repo.DeleteAgent(t.Context(), "a")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("agent protocol state remains: %v", err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "cleanup_unconfirmed" ||
		!deployment.CleanupConfirmedAt.Valid || deployment.AssignedAgentID.Valid {
		t.Fatalf("cleanup history=%+v error=%v", deployment, err)
	}
	unconfirmed, err := repo.Queries.HasUnconfirmedContainerCleanup(
		t.Context(),
		1,
	)
	if err != nil || unconfirmed != 0 {
		t.Fatalf("cleanup blocked=%d error=%v", unconfirmed, err)
	}
	blockers, err := repo.Queries.ListEnvironmentQueueBlockers(t.Context(), 1)
	if err != nil || len(blockers) != 0 {
		t.Fatalf("environment blockers=%v error=%v", blockers, err)
	}
}

func TestDeletedAgentCanPairAsNew(t *testing.T) {
	// Given: deletion forgets even the previous identity and pairing tuple.
	repo := remoteFixture(t)
	if err := repo.DeleteAgent(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	tuple := repository.AgentPairingTuple{
		AgentID: "new-registration", AgentName: "rejoined agent",
		Endpoint: "https://rejoined.invalid", AgentPin: strings.Repeat("a", 64),
		PairingCodeHash:     bytes.Repeat([]byte{1}, 32),
		AgentPublicIdentity: "certificate", ServerPublicIdentity: "server",
		ServerPin: strings.Repeat("c", 64), EncryptedIdentity: "encrypted",
		Now: 200, ExpiresAt: 500,
	}

	// When: the same identity is explicitly paired again.
	pairing, err := repo.PrepareAgentPairing(t.Context(), tuple)

	// Then: it is a new registration, and the old ID stays absent.
	if err != nil || pairing.AgentID != tuple.AgentID ||
		pairing.State != "committing" {
		t.Fatalf("pairing=%+v error=%v", pairing, err)
	}
	if _, err := repo.Queries.GetAgent(t.Context(), "a"); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("old lookup error=%v", err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.AssignedAgentID.Valid {
		t.Fatalf("history=%+v error=%v", deployment, err)
	}
}

func assertAgentDeletionRecordsRemoved(
	t *testing.T,
	repo *repository.Repository,
) {
	t.Helper()
	for _, table := range []string{
		"agent_pairings", "agent_labels", "agent_interpreters",
		"agent_environment_labels", "agent_execution_modes",
		"agent_container_runtimes", "agent_container_interpreters",
		"remote_deployment_claims", "remote_step_runs", "remote_step_log_sequences",
	} {
		var count int
		if err := repo.DB.QueryRow("SELECT COUNT(*) FROM " + table +
			" WHERE agent_id='a'").Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s retained agent rows=%d error=%v", table, count, err)
		}
	}
}

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
	if err != nil || deployment.AssignedAgentID.String != "a" ||
		deployment.Status != "failed" {
		t.Fatalf("history=%+v error=%v", deployment, err)
	}
	rows, err := repo.ClaimRemoteDeployment(t.Context(), claimArg("a"))
	if err != nil || rows != 0 {
		t.Fatalf("claim rows=%d error=%v", rows, err)
	}
	rows, err = repo.Queries.HeartbeatAgent(
		t.Context(),
		db.HeartbeatAgentParams{
			ID: "a", Now: ni(200), CertificateFingerprint: ns(strings.Repeat("a", 64)),
		},
	)
	if err != nil || rows != 0 {
		t.Fatalf("heartbeat rows=%d error=%v", rows, err)
	}
}

func TestAgentDeleteRejectsUnresolvedWorkWithoutRevoking(t *testing.T) {
	for _, table := range []string{"remote_step_runs", "remote_deployment_claims"} {
		for _, state := range []string{
			"started", "lost", "cancel_unconfirmed", "cleanup_unconfirmed",
		} {
			t.Run(table+"/"+state, func(t *testing.T) {
				// Given
				repo := remoteFixture(t)
				if table == "remote_step_runs" {
					if _, err := repo.DB.Exec(`INSERT INTO remote_step_runs
						(deployment_id,step_index,agent_id,state)
						VALUES(1,0,'a','waiting')`); err != nil {
						t.Fatal(err)
					}
				}
				var finished, cancellation any
				if state != "started" {
					finished = 101
				}
				if state == "cancel_unconfirmed" {
					cancellation = 100
				}
				if _, err := repo.DB.Exec("UPDATE "+table+`
					SET state=?,claim_token_hash=zeroblob(32),ciphertext='payload',
					claim_expires_at=200,last_heartbeat_at=100,started_at=100,
					finished_at=?,cancel_requested_at=?
					WHERE agent_id='a' AND deployment_id=1`,
					state, finished, cancellation); err != nil {
					t.Fatal(err)
				}

				// When
				err := repo.DeleteAgent(t.Context(), "a")

				// Then
				if !errors.Is(err, repository.ErrAgentDeletionBlocked) {
					t.Fatalf("delete error=%v", err)
				}
				agent, err := repo.Queries.GetAgent(t.Context(), "a")
				if err != nil || agent.Status != "active" {
					t.Fatalf("agent=%+v error=%v", agent, err)
				}
				var got string
				if err := repo.DB.QueryRow("SELECT state FROM " + table +
					" WHERE agent_id='a' AND deployment_id=1").Scan(&got); err != nil || got != state {
					t.Fatalf("work state=%q error=%v", got, err)
				}
			})
		}
	}
}

func TestAgentDeleteAllowsConfirmedCleanup(t *testing.T) {
	// Given: completed cleanup retains its original terminal history.
	repo := remoteFixture(t)
	if _, err := repo.DB.Exec(`UPDATE remote_deployment_claims
		SET state='cleanup_unconfirmed',claim_token_hash=zeroblob(32),
		ciphertext='payload',claim_expires_at=200,last_heartbeat_at=100,
		started_at=100,finished_at=101,cleanup_confirmed_at=200
		WHERE deployment_id=1`); err != nil {
		t.Fatal(err)
	}

	// When
	err := repo.DeleteAgent(t.Context(), "a")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	claim, err := repo.Queries.GetRemoteDeploymentClaim(t.Context(), 1)
	if err != nil || claim.State != "cleanup_unconfirmed" ||
		claim.CleanupConfirmedAt.Int64 != 200 {
		t.Fatalf("history=%+v error=%v", claim, err)
	}
}

func TestAgentDeletePreservesBufferedTerminalLogs(t *testing.T) {
	for _, table := range []string{"remote_step_runs", "remote_deployment_claims"} {
		for _, state := range []string{"succeeded", "failed", "cancelled"} {
			t.Run(table+"/"+state, func(t *testing.T) {
				// Given: terminal remote work still has logs awaiting a flush.
				repo := remoteFixture(t)
				if table == "remote_step_runs" {
					if _, err := repo.DB.Exec(`INSERT INTO remote_step_runs
						(deployment_id,step_index,agent_id,state)
						VALUES(1,0,'a','waiting')`); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := repo.DB.Exec("UPDATE "+table+`
					SET state=?,claim_token_hash=zeroblob(32),ciphertext='payload',
					claim_expires_at=200,last_heartbeat_at=100,started_at=100,
					finished_at=101,cancel_requested_at=CASE WHEN ?='cancelled' THEN 100 END,
					log_buffer_ciphertext='buffered-logs' WHERE deployment_id=1`,
					state, state); err != nil {
					t.Fatal(err)
				}

				// When
				err := repo.DeleteAgent(t.Context(), "a")

				// Then: access and the buffered history survive the refused delete.
				if !errors.Is(err, repository.ErrAgentDeletionBlocked) {
					t.Fatalf("delete error=%v", err)
				}
				agent, err := repo.Queries.GetAgent(t.Context(), "a")
				if err != nil || agent.Status != "active" {
					t.Fatalf("agent=%+v error=%v", agent, err)
				}
				var buffer string
				if err := repo.DB.QueryRow("SELECT log_buffer_ciphertext FROM " +
					table + " WHERE deployment_id=1").Scan(&buffer); err != nil ||
					buffer != "buffered-logs" {
					t.Fatalf("buffer=%q error=%v", buffer, err)
				}
				if _, err := repo.DB.Exec("UPDATE " + table +
					" SET log_buffer_ciphertext=NULL WHERE deployment_id=1"); err != nil {
					t.Fatal(err)
				}
				if err := repo.DeleteAgent(t.Context(), "a"); err != nil {
					t.Fatalf("delete after logs flushed: %v", err)
				}
			})
		}
	}
}

func TestDeletedAgentPairingCannotBeReplayed(t *testing.T) {
	// Given
	repo := remoteFixture(t)
	if err := repo.DeleteAgent(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	tuple := repository.AgentPairingTuple{
		AgentID: "replacement", AgentName: "replacement",
		Endpoint:        "https://different.invalid",
		PairingCodeHash: bytes.Repeat([]byte{1}, 32),
		AgentPin:        strings.Repeat("a", 64),
	}

	// When
	_, err := repo.PrepareAgentPairing(t.Context(), tuple)

	// Then
	if !errors.Is(err, repository.ErrPairingTupleConflict) {
		t.Fatalf("pairing error=%v", err)
	}
}

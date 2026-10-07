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
			ID:                     "a",
			Now:                    ni(200),
			CertificateFingerprint: ns(strings.Repeat("a", 64)),
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
					table + " WHERE deployment_id=1").
					Scan(&buffer); err != nil ||
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
	// A retry that read before deletion must still reject the consumed code.
	if _, err := repo.Queries.ResetRevokedAgentPairing(t.Context(),
		db.ResetRevokedAgentPairingParams{
			AgentID: "a", PairingCodeHash: tuple.PairingCodeHash,
			AgentPublicIdentity: "public", AgentPin: tuple.AgentPin,
			ServerPublicIdentity: ns("server"),
			ServerPin:            ns(strings.Repeat("b", 64)),
			EncryptedIdentity:    ns("identity"), Now: 200, ExpiresAt: 500,
		}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale SQL retry accepted deleted code: %v", err)
	}
}

func TestDeletedAgentCanRejoinWithFreshPairing(t *testing.T) {
	// Given: the same installation retains its certificate after deletion.
	repo := remoteFixture(t)
	if err := repo.DeleteAgent(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	tuple := repository.AgentPairingTuple{
		AgentID: "unused-new-id", AgentName: "rejoined agent",
		Endpoint: "https://rejoined.invalid", AgentPin: strings.Repeat("a", 64),
		PairingCodeHash:     bytes.Repeat([]byte{9}, 32),
		AgentPublicIdentity: "certificate", ServerPublicIdentity: "server",
		ServerPin: strings.Repeat("c", 64), EncryptedIdentity: "encrypted",
		Now: 200, ExpiresAt: 500,
	}

	// When: an administrator starts a fresh pairing with the original pin.
	pairing, err := repo.PrepareAgentPairing(t.Context(), tuple)

	// Then: fresh preparation preserves the hidden record and history.
	if err != nil || pairing.AgentID != "a" || pairing.State != "committing" {
		t.Fatalf("pairing=%+v error=%v", pairing, err)
	}
	if _, err := repo.Queries.GetAgent(t.Context(), "a"); !errors.Is(
		err, sql.ErrNoRows,
	) {
		t.Fatalf("prepared deleted lookup error=%v", err)
	}
	agent, err := repo.Queries.GetAgentForPairing(t.Context(), "a")
	if err != nil || !agent.DeletedAt.Valid || agent.Status != "revoked" ||
		agent.Name != "a" || agent.CertificateFingerprint.Valid {
		t.Fatalf("agent=%+v error=%v", agent, err)
	}
	deployment, err := repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.AssignedAgentID.String != "a" {
		t.Fatalf("history=%+v error=%v", deployment, err)
	}
	recovered, err := repo.PrepareAgentPairing(t.Context(), tuple)
	if err != nil || recovered.AgentID != pairing.AgentID ||
		!bytes.Equal(recovered.PairingCodeHash, pairing.PairingCodeHash) {
		t.Fatalf("recovered pairing=%+v error=%v", recovered, err)
	}
	conflicting := tuple
	conflicting.PairingCodeHash = bytes.Repeat([]byte{7}, 32)
	if _, err := repo.PrepareAgentPairing(t.Context(), conflicting); !errors.Is(
		err, repository.ErrPairingTupleConflict,
	) {
		t.Fatalf("concurrent pairing overwrite error=%v", err)
	}
	// A caller that read before preparation must also lose at the SQL boundary.
	if _, err := repo.Queries.ResetRevokedAgentPairing(t.Context(),
		db.ResetRevokedAgentPairingParams{
			AgentID:              "a",
			PairingCodeHash:      conflicting.PairingCodeHash,
			AgentPublicIdentity:  tuple.AgentPublicIdentity,
			AgentPin:             tuple.AgentPin,
			ServerPublicIdentity: ns(tuple.ServerPublicIdentity),
			ServerPin: ns(
				tuple.ServerPin,
			),
			EncryptedIdentity: ns("stale"),
			Now:               201,
			ExpiresAt:         500,
		}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("stale SQL reset error=%v", err)
	}
	staleHash := bytes.Repeat([]byte{8}, 32)
	changed, err := repo.CommitAgentPairing(t.Context(),
		db.CompleteAgentPairingParams{
			AgentID: "a", ServerPin: pairing.ServerPin,
			PairingCodeHash: staleHash, Now: ni(201),
		}, db.ActivatePairedAgentParams{})
	assertZero(t, changed, err)
	changed, err = repo.Queries.ExpireCommittingAgentPairing(t.Context(),
		db.ExpireCommittingAgentPairingParams{
			AgentID: "a", PairingCodeHash: staleHash, Now: 201,
		})
	assertZero(t, changed, err)
	changed, err = repo.CommitAgentPairing(t.Context(),
		db.CompleteAgentPairingParams{
			AgentID: "a", ServerPin: pairing.ServerPin,
			PairingCodeHash: pairing.PairingCodeHash, Now: ni(201),
		}, db.ActivatePairedAgentParams{
			CertificatePem:         ns("certificate"),
			CertificateFingerprint: ns(tuple.AgentPin),
		})
	assertOne(t, changed, err)
	agent, err = repo.Queries.GetAgent(t.Context(), "a")
	if err != nil || agent.DeletedAt.Valid || agent.Status != "active" ||
		agent.Name != "a" {
		t.Fatalf("activated agent=%+v error=%v", agent, err)
	}
}

func TestDeletingAgentExpiresIncompletePairing(t *testing.T) {
	repo := remoteFixture(t)
	if _, err := repo.DB.Exec(`UPDATE agent_pairings
		SET state='committing', paired_at=NULL
		WHERE agent_id='a'`); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteAgent(t.Context(), "a"); err != nil {
		t.Fatal(err)
	}
	pairing, err := repo.Queries.GetAgentPairing(t.Context(), "a")
	if err != nil || pairing.State != "expired" {
		t.Fatalf("deleted incomplete pairing=%+v error=%v", pairing, err)
	}
}

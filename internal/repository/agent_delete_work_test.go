package repository_test

import (
	"errors"
	"testing"

	"durpdeploy/internal/repository"
)

func TestAgentDeleteRejectsUnresolvedWorkWithoutRevoking(t *testing.T) {
	for _, table := range []string{"remote_step_runs", "remote_deployment_claims"} {
		for _, state := range []string{
			"started", "lost", "cancel_unconfirmed", "cleanup_unconfirmed",
		} {
			t.Run(table+"/"+state, func(t *testing.T) {
				testAgentDeleteUnresolvedWork(t, table, state)
			})
		}
	}
}

func TestAgentDeletePreservesBufferedTerminalLogs(t *testing.T) {
	for _, table := range []string{"remote_step_runs", "remote_deployment_claims"} {
		for _, state := range []string{"succeeded", "failed", "cancelled"} {
			t.Run(table+"/"+state, func(t *testing.T) {
				testAgentDeleteBufferedLogs(t, table, state)
			})
		}
	}
}

func testAgentDeleteUnresolvedWork(t *testing.T, table, state string) {
	t.Helper()
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
}

func testAgentDeleteBufferedLogs(t *testing.T, table, state string) {
	t.Helper()
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
	if _, err := repo.DB.Exec(`UPDATE deployments SET status='failed',
		finished_at=200 WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	if table == "remote_step_runs" {
		if _, err := repo.DB.Exec(`UPDATE remote_deployment_claims
			SET state='failed',finished_at=200 WHERE deployment_id=1`); err != nil {
			t.Fatal(err)
		}
	}
	if err := repo.DeleteAgent(t.Context(), "a"); err != nil {
		t.Fatalf("delete after logs flushed: %v", err)
	}
}

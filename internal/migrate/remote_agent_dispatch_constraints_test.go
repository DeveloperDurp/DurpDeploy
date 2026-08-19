package migrate

import (
	"bytes"
	"database/sql"
	"testing"
)

func assertRemoteAgentDispatchAndLogConstraints(t *testing.T, conn *sql.DB) {
	t.Helper()
	claimTokenHash := bytes.Repeat([]byte{2}, 32)

	// Given a remote dispatch with a non-null agent claim token hash.
	_, err := conn.Exec(`
		INSERT INTO deployment_dispatches (
			deployment_id, mode, pool_id, selector, state, agent_id, claim_token_hash
		) VALUES (1, 'remote', 1, ?, 'waiting', ?, ?)`,
		"region=us",
		"active-agent",
		claimTokenHash,
	)
	requireNoError(t, err, "create deployment dispatch")
	if _, err := conn.Exec(`
		INSERT INTO deployment_dispatches (deployment_id, mode, pool_id, state)
		VALUES (1, 'remote', 1, 'waiting')`); err == nil {
		t.Fatal("duplicate deployment dispatch succeeded")
	}
	_, err = conn.Exec(`
		INSERT INTO deployments (release_id, environment_id, status)
		VALUES (1, 1, 'pending')`)
	requireNoError(t, err, "create second deployment")

	// When the same claim token hash is reused by another deployment.
	if _, err := conn.Exec(`
		INSERT INTO deployment_dispatches (
			deployment_id, mode, pool_id, state, claim_token_hash
		) VALUES (2, 'remote', 1, 'waiting', ?)`, claimTokenHash); err == nil {
		t.Fatal("duplicate non-null deployment claim token hash succeeded")
	}
	_, err = conn.Exec(`
		INSERT INTO deployments (release_id, environment_id, status)
		VALUES (1, 1, 'pending')`)
	requireNoError(t, err, "create third deployment")
	if _, err := conn.Exec(`
		INSERT INTO deployment_dispatches (deployment_id, mode, pool_id, state)
		VALUES (3, 'remote', 1, 'unknown')`); err == nil {
		t.Fatal("dispatch with unknown protocol state succeeded")
	}

	// Then sequence is required and explicit sequences stay unique.
	if _, err := conn.Exec(`
		INSERT INTO deployment_logs (deployment_id, step_name, line)
		VALUES (1, ?, ?)`, "remote-step", "missing"); err == nil {
		t.Fatal("omitted deployment log sequence succeeded")
	}
	_, err = conn.Exec(`
		INSERT INTO deployment_logs (deployment_id, step_name, line, sequence)
		VALUES (1, ?, ?, 1)`, "remote-step", "first")
	requireNoError(t, err, "create sequenced deployment log")
	if _, err := conn.Exec(`
		INSERT INTO deployment_logs (deployment_id, step_name, line, sequence)
		VALUES (1, ?, ?, 1)`, "remote-step", "duplicate"); err == nil {
		t.Fatal("duplicate non-null deployment log sequence succeeded")
	}

	_, err = conn.Exec(`
		INSERT INTO agent_events (
			agent_id, deployment_id, event_type, dispatch_state
		) VALUES (?, 1, ?, 'claimed')`,
		"active-agent",
		"claimed",
	)
	requireNoError(t, err, "append agent event")
	if _, err := conn.Exec(`
		INSERT INTO agent_events (
			agent_id, deployment_id, event_type, dispatch_state
		) VALUES (?, 1, ?, 'unknown')`,
		"active-agent",
		"invalid",
	); err == nil {
		t.Fatal("agent event with unknown protocol state succeeded")
	}
}

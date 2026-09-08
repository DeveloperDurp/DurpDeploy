package migrate

import (
	"bytes"
	"database/sql"
	"testing"
)

func assertRemoteLifecycle(t *testing.T, conn *sql.DB) {
	t.Helper()
	_, err := conn.Exec(`UPDATE deployment_step_attempts
		SET state='claimed', agent_id='a', claim_token_hash=?,
		claim_expires_at=60 WHERE step_index=0`, bytes.Repeat([]byte{32}, 32))
	requireNoError(t, err, "claim first step")
	_, err = conn.Exec(`UPDATE deployment_step_attempts
		SET state='claimed', agent_id='a', claim_token_hash=?,
		claim_expires_at=60 WHERE step_index=1`, bytes.Repeat([]byte{33}, 32))
	if err == nil {
		t.Fatal("agent claimed two active attempts")
	}
	t.Logf("capacity rejected second claim: %v", err)
	for _, state := range []string{
		"started", "cancel_requested", "cancel_uncertain", "lost",
		"succeeded", "failed", "cancelled",
	} {
		var finished sql.NullInt64
		if state != "started" && state != "cancel_requested" {
			finished = sql.NullInt64{Int64: 90, Valid: true}
		}
		_, err := conn.Exec(`UPDATE deployment_step_attempts
			SET state=?, started_at=70, finished_at=?, cancel_requested_at=80
			WHERE step_index=0`, state, finished)
		requireNoError(t, err, "persist state "+state)
		var actual string
		err = conn.QueryRow(`SELECT state FROM deployment_step_attempts
			WHERE deployment_id=1 AND step_index=0 AND attempt=1`).Scan(&actual)
		requireNoError(t, err, "read persisted state")
		if actual != state {
			t.Fatalf("state=%s want=%s", actual, state)
		}
		t.Logf("persisted state=%s", actual)
	}
	_, err = conn.Exec(`INSERT INTO deployment_step_attempts
		(deployment_id,step_index,attempt,wait_deadline) VALUES(1,0,2,400)`)
	requireNoError(t, err, "new attempt after terminal outcome")
	_, err = conn.Exec(`INSERT INTO deployment_log_scopes
		(log_id,deployment_id,step_index,attempt,sequence) VALUES(11,1,0,2,1)`)
	requireNoError(t, err, "same sequence in a different attempt")
	_, err = conn.Exec(`INSERT INTO agent_pairings
		(agent_id,pairing_code_hash,agent_public_identity,agent_pin,
		expires_at) VALUES('a',?,'public',?,300)`,
		bytes.Repeat([]byte{32}, 32), string(bytes.Repeat([]byte{'a'}, 64)))
	requireNoError(t, err, "pending pairing with binary hash ending in space")
	if _, err := conn.Exec(`UPDATE agent_pairings
		SET state='committing' WHERE agent_id='a'`); err == nil {
		t.Fatal("committing pairing without recoverable identity accepted")
	}
	_, err = conn.Exec(`UPDATE agent_pairings
		SET state='committing', encrypted_identity='encrypted' WHERE agent_id='a'`)
	requireNoError(t, err, "persist recoverable committing state")
	if _, err := conn.Exec(`UPDATE agent_pairings
		SET state='paired' WHERE agent_id='a'`); err == nil {
		t.Fatal("paired state without server identity accepted")
	}
	t.Log("PASS: states, agent capacity, retry log scope, pairing constraints")
}

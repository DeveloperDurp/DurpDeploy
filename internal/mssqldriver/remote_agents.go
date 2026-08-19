package mssqldriver

import "strings"

func rewriteRemoteAgentConflict(query string) (string, bool) {
	const payload = "INSERT INTO deployment_payloads (deployment_id, ciphertext)"
	if start := strings.Index(
		query,
		payload,
	); start >= 0 &&
		strings.Contains(query, "ON CONFLICT(deployment_id) DO UPDATE") {
		return query[:start] + `MERGE deployment_payloads WITH (HOLDLOCK) AS target
USING (SELECT @p1 AS deployment_id, @p2 AS ciphertext) AS source
ON target.deployment_id = source.deployment_id
WHEN NOT MATCHED THEN INSERT (deployment_id, ciphertext)
VALUES (source.deployment_id, source.ciphertext)
WHEN MATCHED THEN UPDATE SET deployment_id = target.deployment_id
OUTPUT INSERTED.deployment_id, INSERTED.ciphertext, INSERTED.created_at;`, true
	}
	const dispatch = "INSERT INTO deployment_dispatches (deployment_id, mode, pool_id, selector, state, reason)"
	if start := strings.Index(
		query,
		dispatch,
	); start >= 0 &&
		strings.Contains(query, "ON CONFLICT(deployment_id) DO UPDATE") {
		return query[:start] + `MERGE deployment_dispatches WITH (HOLDLOCK) AS target
USING (SELECT @p1 AS deployment_id, @p2 AS mode, @p3 AS pool_id, @p4 AS selector, @p5 AS state, @p6 AS reason) AS source
ON target.deployment_id = source.deployment_id
WHEN NOT MATCHED THEN INSERT (deployment_id, mode, pool_id, selector, state, reason)
VALUES (source.deployment_id, source.mode, source.pool_id, source.selector, source.state, source.reason)
WHEN MATCHED THEN UPDATE SET deployment_id = target.deployment_id
OUTPUT INSERTED.deployment_id, INSERTED.mode, INSERTED.pool_id, INSERTED.selector, INSERTED.state, INSERTED.reason, INSERTED.agent_id, INSERTED.claim_token_hash, INSERTED.claim_expires_at, INSERTED.started_at, INSERTED.finished_at, INSERTED.last_heartbeat_at, INSERTED.cancel_requested_at, INSERTED.created_at, INSERTED.updated_at;`, true
	}
	const insert = "INSERT INTO deployment_logs (deployment_id, step_name, line, sequence)"
	const conflict = "ON CONFLICT(deployment_id, sequence) DO UPDATE"
	start := strings.Index(query, insert)
	if start < 0 || !strings.Contains(query, conflict) {
		return "", false
	}
	return query[:start] + `MERGE deployment_logs WITH (HOLDLOCK) AS target
USING (SELECT @p1 AS deployment_id, @p2 AS step_name, @p3 AS line, @p4 AS sequence) AS source
ON target.deployment_id = source.deployment_id AND target.sequence = source.sequence
WHEN NOT MATCHED BY TARGET AND EXISTS (
    SELECT 1 FROM deployment_dispatches
    WHERE deployment_id = @p1 AND agent_id = @p5
      AND claim_token_hash = @p6 AND state = @p7
) THEN INSERT (deployment_id, step_name, line, sequence)
VALUES (source.deployment_id, source.step_name, source.line, source.sequence)
WHEN MATCHED THEN UPDATE SET deployment_id = target.deployment_id
OUTPUT INSERTED.id, INSERTED.deployment_id, INSERTED.step_name, INSERTED.line,
       INSERTED.created_at, INSERTED.sequence;`, true
}

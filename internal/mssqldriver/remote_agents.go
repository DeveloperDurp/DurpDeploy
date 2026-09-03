package mssqldriver

import "strings"

func rewriteRemoteAgentConflict(query string) (string, bool) {
	const assignment = "INSERT INTO environment_agent_assignments ("
	if start := strings.Index(query, assignment); start >= 0 &&
		strings.Contains(query, "ON CONFLICT(environment_id) DO UPDATE") {
		return query[:start] + `MERGE environment_agent_assignments WITH (HOLDLOCK) AS target
	USING (SELECT @p1 AS environment_id, @p2 AS agent_id, @p3 AS updated_at) AS source
	ON target.environment_id = source.environment_id
	WHEN NOT MATCHED THEN INSERT (environment_id, agent_id, updated_at)
	VALUES (source.environment_id, source.agent_id, source.updated_at)
	WHEN MATCHED THEN UPDATE SET agent_id = source.agent_id, updated_at = source.updated_at
	OUTPUT INSERTED.environment_id, INSERTED.agent_id, INSERTED.created_at, INSERTED.updated_at;`, true
	}
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

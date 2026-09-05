package mssqldriver

import "strings"

func rewriteAgentLabelRouting(query string) (string, bool) {
	const membership = "INSERT INTO agent_label_memberships (agent_label_id, agent_id)"
	if !strings.Contains(query, membership) ||
		!strings.Contains(query, "RETURNING agent_label_id, agent_id, created_at") {
		return "", false
	}
	start := strings.Index(query, membership)
	return query[:start] + `INSERT INTO agent_label_memberships (
    agent_label_id, agent_id
)
OUTPUT INSERTED.agent_label_id, INSERTED.agent_id, INSERTED.created_at
SELECT @p1, @p2
WHERE EXISTS (
    SELECT 1
    FROM agents
    JOIN agent_pairings ON agent_pairings.agent_id = agents.id
    WHERE agents.id = @p2
      AND agents.status = 'active'
      AND agent_pairings.state = 'paired'
);`, true
}

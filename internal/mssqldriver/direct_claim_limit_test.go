package mssqldriver

import (
	"strings"
	"testing"
)

func TestRewriteSQL_DirectClaimLimitKeepsOuterGuards(t *testing.T) {
	query := `UPDATE deployment_dispatches
SET state = 'claimed',
    agent_id = ?1,
    updated_at = unixepoch()
WHERE deployment_id = (
    SELECT candidate.deployment_id
    FROM deployment_dispatches AS candidate
    WHERE candidate.assigned_agent_id = ?1
    ORDER BY candidate.created_at ASC, candidate.deployment_id ASC
    LIMIT 1
)
AND assigned_agent_id = ?1
AND state = 'waiting'
RETURNING deployment_id`

	got, err := RewriteSQL(query)
	if err != nil {
		t.Fatalf("RewriteSQL() error = %v", err)
	}
	if strings.Contains(got, "LIMIT") {
		t.Fatalf("RewriteSQL() retained SQLite LIMIT: %q", got)
	}
	if !strings.Contains(got, "SELECT TOP (1) candidate.deployment_id") {
		t.Fatalf("RewriteSQL() did not add nested TOP (1): %q", got)
	}
	if !strings.Contains(
		got,
		")\nAND assigned_agent_id = @p1\nAND state = 'waiting'",
	) {
		t.Fatalf("RewriteSQL() lost outer claim guards: %q", got)
	}
}

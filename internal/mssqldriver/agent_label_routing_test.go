package mssqldriver_test

import (
	"io/fs"
	"strings"
	"testing"

	"durpdeploy/internal/mssqldriver"
	"durpdeploy/migrations"
)

func TestAgentLabelRoutingQueriesRewriteMembership(t *testing.T) {
	query := `-- name: CreateAgentLabelMembership :one
INSERT INTO agent_label_memberships (agent_label_id, agent_id)
SELECT ?, ?
WHERE EXISTS (
    SELECT 1 FROM agents JOIN agent_pairings
      ON agent_pairings.agent_id = agents.id
    WHERE agents.id = ? AND agents.status = 'active'
      AND agent_pairings.state = 'paired'
)
RETURNING agent_label_id, agent_id, created_at`

	got, err := mssqldriver.RewriteSQL(query)
	if err != nil {
		t.Fatalf("rewrite membership: %v", err)
	}
	for _, required := range []string{
		"OUTPUT INSERTED.agent_label_id",
		"SELECT @p1, @p2",
		"agents.status = 'active'",
		"agent_pairings.state = 'paired'",
	} {
		if !strings.Contains(got, required) {
			t.Fatalf("rewritten membership missing %q: %s", required, got)
		}
	}
	if strings.Contains(got, "RETURNING") {
		t.Fatalf("rewritten membership retained RETURNING: %s", got)
	}
}

func TestAgentLabelRoutingMigrationSourceShapes(t *testing.T) {
	migrationFS, err := fs.Sub(migrations.MSSQLFS, "mssql")
	if err != nil {
		t.Fatalf("open MSSQL migrations: %v", err)
	}
	source, err := fs.ReadFile(migrationFS, "017_agent_label_routing.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	text := string(source)
	for _, required := range []string{
		"uq_agent_labels_normalized_name",
		"ck_project_execution_policy",
		"deployments_root_parent_guard",
		"scheduled_deployment_occurrences",
		"cannot downgrade agent label routing with fan-out history",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("MSSQL migration missing %q", required)
		}
	}
	if strings.Contains(text, "direct_dispatch_backup") {
		t.Fatal("MSSQL migration revived removed direct-dispatch backups")
	}
}

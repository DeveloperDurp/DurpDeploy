package mssqldriver_test

import (
	"strings"
	"testing"

	"durpdeploy/internal/mssqldriver"
)

func TestDeploymentReturningSupportsParentGuardTrigger(t *testing.T) {
	for _, statement := range []string{
		"INSERT INTO deployments (release_id, environment_id, status) VALUES (?, ?, ?)",
		"UPDATE deployments SET status = ? WHERE id = ?",
	} {
		got, err := mssqldriver.RewriteSQL(statement + " RETURNING id, status;")
		if err != nil {
			t.Fatal(err)
		}
		for _, clause := range []string{
			"DECLARE @deployment_ids TABLE (id BIGINT)",
			"OUTPUT INSERTED.id INTO @deployment_ids",
			"SELECT id, status FROM deployments",
			"WITH (FORCESEEK)",
			"WHERE id IN (SELECT id FROM @deployment_ids)",
		} {
			if !strings.Contains(got, clause) {
				t.Fatalf("trigger-safe RETURNING lacks %q: %s", clause, got)
			}
		}
	}
}

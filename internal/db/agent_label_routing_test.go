package db_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"durpdeploy/internal/migrate"
	"durpdeploy/migrations"
)

func TestAgentLabelRoutingSchemaBaseline(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	defer conn.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.UpTo(conn, ".", 31); err != nil {
		t.Fatalf("migrate to version 31: %v", err)
	}

	var version int64
	if err := conn.QueryRow(
		"SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1",
	).Scan(&version); err != nil {
		t.Fatalf("read migration version: %v", err)
	}
	if version != 31 {
		t.Fatalf("migration version = %d, want 31", version)
	}

	fixture := []string{
		"INSERT INTO projects (name) VALUES ('baseline')",
		"INSERT INTO environments (name) VALUES ('baseline')",
		"INSERT INTO releases (project_id, version, steps_json) VALUES (1, 'baseline', '[]')",
		"INSERT INTO deployments (release_id, environment_id, status) VALUES (1, 1, 'pending')",
	}
	for _, statement := range fixture {
		if _, err := conn.ExecContext(context.Background(), statement); err != nil {
			t.Fatalf("baseline fixture %q: %v", statement, err)
		}
	}
	rows, err := conn.QueryContext(
		context.Background(),
		"SELECT id FROM deployments ORDER BY created_at DESC",
	)
	if err != nil {
		t.Fatalf("list deployments: %v", err)
	}
	defer rows.Close()
	if !rows.Next() {
		t.Fatal("root deployment was not listed")
	}
	var id int64
	if err := rows.Scan(&id); err != nil {
		t.Fatalf("scan deployment: %v", err)
	}
	if id != 1 || rows.Next() {
		t.Fatal("baseline deployment rows did not contain only root 1")
	}
}

func TestAgentLabelRoutingSchemaConstraints(t *testing.T) {
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()

	statements := []string{
		"INSERT INTO agent_labels (name, normalized_name) VALUES ('Cat Fact', 'cat fact')",
		"INSERT INTO agent_labels (name, normalized_name) VALUES ('Build', 'build')",
		"INSERT INTO projects (name) VALUES ('routing')",
		"INSERT INTO projects (name) VALUES ('invalid-local')",
		"INSERT INTO projects (name) VALUES ('invalid-label')",
		"INSERT INTO projects (name) VALUES ('invalid-mode')",
		"INSERT INTO projects (name) VALUES ('invalid-strategy')",
		"INSERT INTO project_execution_policies (project_id, target_mode) VALUES (1, 'local')",
	}
	for _, statement := range statements {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("execute %q: %v", statement, err)
		}
	}

	invalid := []string{
		"INSERT INTO agent_labels (name, normalized_name) VALUES (' CAT FACT ', 'cat fact')",
		"INSERT INTO agent_label_memberships (agent_label_id, agent_id) VALUES (1, 'missing')",
		"INSERT INTO project_execution_policies (project_id, target_mode, agent_label_id, agent_strategy) VALUES (2, 'local', 1, 'all')",
		"INSERT INTO project_execution_policies (project_id, target_mode, agent_label_id, agent_strategy) VALUES (3, 'label', NULL, 'round_robin')",
		"INSERT INTO project_execution_policies (project_id, target_mode) VALUES (4, 'remote')",
		"INSERT INTO project_execution_policies (project_id, target_mode, agent_label_id, agent_strategy) VALUES (5, 'label', 1, 'random')",
	}
	for _, statement := range invalid {
		if _, err := conn.Exec(statement); err == nil {
			t.Fatalf("invalid statement succeeded: %q", statement)
		} else {
			t.Logf("constraint rejected %q: %v", statement, err)
		}
	}

	var legacyPolicies int
	if err := conn.QueryRow(
		"SELECT COUNT(*) FROM project_execution_policies",
	).Scan(&legacyPolicies); err != nil {
		t.Fatalf("count policies: %v", err)
	}
	if legacyPolicies != 1 {
		t.Fatalf("policy count = %d, want only explicit policy", legacyPolicies)
	}
}

func TestAgentLabelRoutingMigrationRollbackGuard(t *testing.T) {
	dsn := t.TempDir() + "/rollback.db?_pragma=foreign_keys(1)"
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()

	fixture := []string{
		"INSERT INTO projects (name) VALUES ('rollback')",
		"INSERT INTO environments (name) VALUES ('rollback')",
		"INSERT INTO releases (project_id, version, steps_json) VALUES (1, 'v1', '[]')",
		"INSERT INTO deployments (release_id, environment_id, status) VALUES (1, 1, 'pending')",
		"INSERT INTO deployments (release_id, environment_id, status, parent_deployment_id) VALUES (1, 1, 'pending', 1)",
	}
	for _, statement := range fixture {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("fixture %q: %v", statement, err)
		}
	}

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	err = goose.DownTo(conn, ".", 31)
	if err == nil || !strings.Contains(err.Error(), "fan-out") {
		t.Fatalf("downgrade error = %v, want fan-out guard", err)
	}
	t.Logf("unsafe downgrade rejected: %v", err)

	var version int64
	if err := conn.QueryRow(
		"SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1",
	).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 32 {
		t.Fatalf("version after rejected downgrade = %d, want 32", version)
	}
}

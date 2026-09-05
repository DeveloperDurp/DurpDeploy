package migrate

import (
	"database/sql"
	"testing"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite"

	"durpdeploy/migrations"
)

func TestAgentLabelRoutingMigrationUpgradeFrom31(t *testing.T) {
	dsn := t.TempDir() + "/upgrade.db?_pragma=foreign_keys(1)"
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open SQLite: %v", err)
	}
	defer conn.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.UpTo(conn, ".", 31); err != nil {
		t.Fatalf("migrate to 31: %v", err)
	}
	fixture := []string{
		"INSERT INTO projects (name) VALUES ('legacy')",
		"INSERT INTO environments (name) VALUES ('legacy')",
		"INSERT INTO releases (project_id, version, steps_json) VALUES (1, 'v1', '[]')",
		"INSERT INTO deployments (release_id, environment_id, status) VALUES (1, 1, 'succeeded')",
	}
	for _, statement := range fixture {
		if _, err := conn.Exec(statement); err != nil {
			t.Fatalf("legacy fixture %q: %v", statement, err)
		}
	}
	if err := goose.Up(conn, "."); err != nil {
		t.Fatalf("upgrade to 32: %v", err)
	}

	var deploymentID int64
	if err := conn.QueryRow("SELECT id FROM deployments").Scan(
		&deploymentID,
	); err != nil {
		t.Fatalf("read preserved deployment: %v", err)
	}
	if deploymentID != 1 {
		t.Fatalf("deployment ID = %d, want 1", deploymentID)
	}
	var policies int
	if err := conn.QueryRow(
		"SELECT COUNT(*) FROM project_execution_policies",
	).Scan(&policies); err != nil {
		t.Fatalf("count policies: %v", err)
	}
	if policies != 0 {
		t.Fatalf("legacy policy count = %d, want 0", policies)
	}
}

func TestAgentLabelRoutingMigrationCleanRollback(t *testing.T) {
	dsn := t.TempDir() + "/rollback.db?_pragma=foreign_keys(1)"
	conn, err := Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()

	goose.SetBaseFS(migrations.FS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.DownTo(conn, ".", 31); err != nil {
		t.Fatalf("clean downgrade: %v", err)
	}
	var version int64
	if err := conn.QueryRow(
		"SELECT MAX(version_id) FROM goose_db_version WHERE is_applied = 1",
	).Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 31 {
		t.Fatalf("downgraded version = %d, want 31", version)
	}
}

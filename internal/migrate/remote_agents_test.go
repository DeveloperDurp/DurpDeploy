package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"durpdeploy/migrations"
)

func TestRemoteAgentLegacyDeploymentLogsHaveNoSequence(t *testing.T) {
	// Given the schema immediately before the remote-agent migration.
	conn, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	if err := goose.SetDialect("sqlite"); err != nil {
		t.Fatalf("set SQLite dialect: %v", err)
	}
	if err := goose.UpTo(conn, ".", 24); err != nil {
		t.Fatalf("migrate legacy schema: %v", err)
	}

	_, err = conn.Exec(`
		INSERT INTO projects (name) VALUES ('legacy-project');
		INSERT INTO environments (name) VALUES ('legacy-environment');
		INSERT INTO releases (project_id, version, steps_json)
		VALUES (1, 'legacy-release', '[]');
		INSERT INTO deployments (release_id, environment_id, status)
		VALUES (1, 1, 'pending');
		INSERT INTO deployment_logs (deployment_id, step_name, line)
		VALUES (1, 'legacy-step', 'legacy line');`)
	if err != nil {
		t.Fatalf("seed legacy deployment log: %v", err)
	}

	// When the historic row is read through an explicit column list.
	var line string
	err = conn.QueryRow(`
		SELECT line FROM deployment_logs
		WHERE deployment_id = 1 AND id = 1`).Scan(&line)
	if err != nil {
		t.Fatalf("read legacy deployment log: %v", err)
	}

	// Then the history is readable and the legacy shape has no sequence column.
	if line != "legacy line" {
		t.Fatalf(
			"legacy deployment log line = %q, want %q",
			line,
			"legacy line",
		)
	}
	rows, err := conn.Query("PRAGMA table_info(deployment_logs)")
	if err != nil {
		t.Fatalf("inspect legacy deployment_logs: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(
			&cid,
			&name,
			&columnType,
			&notNull,
			&defaultValue,
			&primaryKey,
		); err != nil {
			t.Fatalf("scan legacy deployment_logs column: %v", err)
		}
		if name == "sequence" {
			t.Fatal("legacy deployment_logs unexpectedly has sequence")
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate legacy deployment_logs columns: %v", err)
	}
}

func TestRemoteAgentSchemaCreatesControlPlaneTables(t *testing.T) {
	// Given the latest schema.
	dsn := "file:" + filepath.Join(t.TempDir(), "remote-agents.db") +
		"?_pragma=foreign_keys(1)"
	conn, err := Run(dsn)
	if err != nil {
		t.Fatalf("migrate latest schema: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// When each durable remote-agent structure is inspected.
	for _, table := range []string{
		"agent_pools",
		"agents",
		"agent_pool_memberships",
		"agent_tags",
		"environment_agent_policies",
		"agent_enrollment_tokens",
		"deployment_payloads",
		"deployment_dispatches",
		"agent_events",
	} {
		var count int
		err := conn.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&count)
		if err != nil {
			t.Fatalf("find remote-agent table %s: %v", table, err)
		}

		// Then every table is present exactly once.
		if count != 1 {
			t.Fatalf(
				"remote-agent table %s count = %d, want 1",
				table,
				count,
			)
		}
	}
	assertRemoteAgentIndexes(t, conn, `
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'index' AND name = ?`)
	assertRemoteAgentPersistence(t, conn)
}

func TestSQLite_RemoteAgentHistoricLogsBackfill(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		// Given a fresh database migrated through the remote-agent schema.
		conn := newRemoteAgentSQLiteDB(t)
		migrateRemoteAgentSQLiteTo(t, conn, 25)

		// When the latest migration is rolled back and applied again.
		if err := goose.Down(conn, "."); err != nil {
			t.Fatalf("roll back remote-agent migration: %v", err)
		}
		if err := goose.Up(conn, "."); err != nil {
			t.Fatalf("reapply remote-agent migration: %v", err)
		}

		// Then the control-plane table returns after the round trip.
		var count int
		err := conn.QueryRow(`
			SELECT COUNT(*) FROM sqlite_master
			WHERE type = 'table' AND name = 'agent_pools'`).Scan(&count)
		if err != nil {
			t.Fatalf("find agent_pools after reapply: %v", err)
		}
		if count != 1 {
			t.Fatalf("agent_pools after reapply = %d, want 1", count)
		}
	})

	t.Run("populated", func(t *testing.T) {
		// Given a version-24 database with historic deployment logs.
		conn := newRemoteAgentSQLiteDB(t)
		migrateRemoteAgentSQLiteTo(t, conn, 24)
		_, err := conn.Exec(`
			INSERT INTO projects (name) VALUES ('backfill-project');
			INSERT INTO environments (name) VALUES ('backfill-environment');
			INSERT INTO releases (project_id, version, steps_json)
			VALUES (1, 'backfill-release', '[]');
			INSERT INTO deployments (release_id, environment_id, status)
			VALUES (1, 1, 'pending');
			INSERT INTO deployment_logs (deployment_id, step_name, line)
			VALUES (1, 'backfill-step', 'first');
			INSERT INTO deployment_logs (deployment_id, step_name, line)
			VALUES (1, 'backfill-step', 'second');`)
		if err != nil {
			t.Fatalf("seed historic deployment logs: %v", err)
		}

		// When the migration is applied, rolled back, and reapplied.
		if err := goose.Up(conn, "."); err != nil {
			t.Fatalf("apply remote-agent migration: %v", err)
		}
		assertDeploymentLogSequences(t, conn, []int64{1, 2})
		if err := goose.Down(conn, "."); err != nil {
			t.Fatalf("roll back remote-agent migration: %v", err)
		}
		var lines int
		err = conn.QueryRow(
			"SELECT COUNT(*) FROM deployment_logs WHERE deployment_id = 1",
		).Scan(&lines)
		if err != nil {
			t.Fatalf("count historic logs after rollback: %v", err)
		}
		if lines != 2 {
			t.Fatalf("historic logs after rollback = %d, want 2", lines)
		}
		if err := goose.Up(conn, "."); err != nil {
			t.Fatalf("reapply remote-agent migration: %v", err)
		}

		// Then the original ids are the ordered, non-null sequences after reapply.
		assertDeploymentLogSequences(t, conn, []int64{1, 2})
	})
}

func newRemoteAgentSQLiteDB(t *testing.T) *sql.DB {
	t.Helper()
	conn, err := sql.Open(
		"sqlite",
		filepath.Join(t.TempDir(), "remote-agents.db"),
	)
	if err != nil {
		t.Fatalf("open SQLite database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func migrateRemoteAgentSQLiteTo(t *testing.T, conn *sql.DB, version int64) {
	t.Helper()
	goose.SetBaseFS(migrations.FS)
	goose.SetTableName("goose_db_version")
	if err := goose.SetDialect("sqlite"); err != nil {
		t.Fatalf("set SQLite dialect: %v", err)
	}
	if err := goose.UpTo(conn, ".", version); err != nil {
		t.Fatalf("migrate SQLite to %d: %v", version, err)
	}
}

func assertDeploymentLogSequences(t *testing.T, conn *sql.DB, want []int64) {
	t.Helper()
	rows, err := conn.Query(`
		SELECT id, sequence FROM deployment_logs
		WHERE deployment_id = 1 ORDER BY id`)
	if err != nil {
		t.Fatalf("read deployment log sequences: %v", err)
	}
	defer rows.Close()
	for index, expected := range want {
		if !rows.Next() {
			t.Fatalf("deployment log sequence %d is missing", index)
		}
		var id, sequence int64
		if err := rows.Scan(&id, &sequence); err != nil {
			t.Fatalf("scan deployment log sequence: %v", err)
		}
		if id != expected || sequence != expected {
			t.Fatalf(
				"deployment log id and sequence = %d, %d; want %d, %d",
				id,
				sequence,
				expected,
				expected,
			)
		}
	}
	if rows.Next() {
		t.Fatal("unexpected deployment log sequence")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate deployment log sequences: %v", err)
	}
}

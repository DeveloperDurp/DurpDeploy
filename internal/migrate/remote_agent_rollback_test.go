package migrate

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/pressly/goose/v3"

	"durpdeploy/migrations"
)

func TestRemoteAgentRollbackRefusal(t *testing.T) {
	for _, version := range []int64{25, 26, 27} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			conn, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
			requireNoError(t, err, "open isolated database")
			defer conn.Close()
			goose.SetBaseFS(migrations.FS)
			requireNoError(t, goose.SetDialect("sqlite3"), "dialect")
			requireNoError(t, goose.UpTo(conn, ".", version), "up")
			if err := goose.Down(conn, "."); err == nil {
				t.Fatal("unsafe down succeeded")
			} else {
				t.Logf("version %d down refused: %v", version, err)
			}
			actual, err := goose.GetDBVersion(conn)
			requireNoError(t, err, "version")
			if actual != version {
				t.Fatalf("version=%d want=%d", actual, version)
			}
			var count int
			err = conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master
				WHERE name LIKE '%rollback_refused'`).Scan(&count)
			requireNoError(t, err, "rollback artifacts")
			if count != 0 {
				t.Fatal("rollback guard leaked after transaction failure")
			}
		})
	}
}

func TestRemoteAgentMigrationFailureIsAtomic(t *testing.T) {
	conn, err := sql.Open("sqlite", ":memory:?_pragma=foreign_keys(1)")
	requireNoError(t, err, "open isolated database")
	defer conn.Close()
	goose.SetBaseFS(migrations.FS)
	requireNoError(t, goose.SetDialect("sqlite3"), "dialect")
	requireNoError(t, goose.UpTo(conn, ".", 24), "legacy schema")
	_, err = conn.Exec(`INSERT INTO projects(id,name) VALUES(1,'legacy');
		INSERT INTO environments(id,name) VALUES(1,'legacy');
		INSERT INTO releases(id,project_id,version,steps_json)
		VALUES(1,1,'bad-json','{broken');
		INSERT INTO deployments(id,release_id,environment_id) VALUES(1,1,1);
		CREATE TABLE agent_labels (unexpected TEXT)`)
	requireNoError(t, err, "seed stale schema and malformed historical JSON")
	if err := goose.Up(conn, "."); err == nil {
		t.Fatal("stale conflicting schema unexpectedly migrated")
	} else {
		t.Logf("mid-migration failure: %v", err)
	}
	var count int
	err = conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master
		WHERE name='agents'`).Scan(&count)
	requireNoError(t, err, "inspect rolled-back DDL")
	if count != 0 {
		t.Fatal("partially created agents table survived rollback")
	}
	version, err := goose.GetDBVersion(conn)
	requireNoError(t, err, "failed migration version")
	if version != 24 {
		t.Fatalf("failed migration advanced version=%d", version)
	}
	_, err = conn.Exec("DROP TABLE agent_labels")
	requireNoError(t, err, "remove only injected fixture conflict")
	requireNoError(t, goose.Up(conn, "."), "retry full migration")
	var frozen string
	err = conn.QueryRow(`SELECT steps_json FROM deployment_step_sources
		WHERE deployment_id=1`).Scan(&frozen)
	requireNoError(t, err, "read malformed legacy snapshot")
	if frozen != "{broken" {
		t.Fatalf("legacy bytes changed=%q", frozen)
	}
	t.Log(
		"PASS: conflicting DDL rolled back atomically; retry preserved malformed JSON",
	)
}

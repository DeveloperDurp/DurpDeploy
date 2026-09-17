package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	"durpdeploy/migrations"
)

func TestRemoteAgentSchemaFreshUpgradeRollback(t *testing.T) {
	t.Run("fresh", func(t *testing.T) {
		conn, err := Run(":memory:?_pragma=foreign_keys(1)")
		requireNoError(t, err, "fresh migrations")
		defer conn.Close()
		assertRemoteTables(t, conn)
	})
	// Given an isolated database containing the schema shipped at version 24.
	path := filepath.Join(t.TempDir(), "remote-agent.db")
	conn, err := sql.Open("sqlite", path+"?_pragma=foreign_keys(1)")
	requireNoError(t, err, "open fixture")
	t.Cleanup(func() {
		requireNoError(t, conn.Close(), "close fixture")
		t.Logf(
			"cleanup: closed isolated fixture %s (t.TempDir removes it)",
			path,
		)
	})
	goose.SetBaseFS(migrations.FS)
	requireNoError(t, goose.SetDialect("sqlite3"), "dialect")
	requireNoError(t, goose.UpTo(conn, ".", 24), "legacy migrations")
	var version int64
	version, err = goose.GetDBVersion(conn)
	requireNoError(t, err, "legacy version")
	if version != 24 {
		t.Fatalf("legacy version=%d", version)
	}
	for _, query := range []string{
		`INSERT INTO projects(id,name) VALUES(1,'legacy')`,
		`INSERT INTO environments(id,name) VALUES(1,'legacy')`,
		`INSERT INTO steps(id,project_id,name,script_body)
		 VALUES(1,1,'legacy','echo legacy')`,
		`INSERT INTO releases(id,project_id,version,steps_json)
		 VALUES(1,1,'v1','[{"name":"legacy","script_body":"echo legacy"}]')`,
		`INSERT INTO deployments(id,release_id,environment_id)
		 VALUES(1,1,1)`,
		`INSERT INTO deployment_logs(id,deployment_id,line)
		 VALUES(7,1,'legacy'),(8,1,'legacy')`,
	} {
		_, err := conn.Exec(query)
		requireNoError(t, err, "seed legacy fixture")
	}
	t.Log(
		"baseline: version=24 releases=1 deployments=1 logs=2; duplicate log text accepted; no sequence column",
	)

	// When the current migrations upgrade it.
	requireNoError(t, goose.Up(conn, "."), "upgrade")

	// Then the new durable table exists.
	var count int
	err = conn.QueryRow("SELECT COUNT(*) FROM agents").Scan(&count)
	requireNoError(t, err, "required agents table")
	assertRemoteTables(t, conn)
	assertRemoteLegacy(t, conn)
	assertRemoteConstraints(t, conn)
	assertRemoteLifecycle(t, conn)
	_, err = conn.Exec(`UPDATE releases SET steps_json = '[]' WHERE id=1`)
	requireNoError(t, err, "refresh source release")
	var frozen, target string
	err = conn.QueryRow(`SELECT steps_json, default_execution_target
		FROM deployment_step_sources WHERE deployment_id=1`).
		Scan(&frozen, &target)
	requireNoError(t, err, "frozen legacy source")
	if frozen != `[{"name":"legacy","script_body":"echo legacy"}]` ||
		target != "local" {
		t.Fatalf("frozen source=%q default=%q", frozen, target)
	}
	t.Log(
		"release refresh preserved deployment source; missing target defaults local",
	)
	requireNoError(t, goose.Down(conn, "."), "rollback remote step runs")
	if err := goose.Down(conn, "."); err == nil {
		t.Fatal("unsafe rollback succeeded")
	} else {
		t.Logf("unsafe rollback refused: %v", err)
	}
	assertRemoteLegacy(t, conn)
	version, err = goose.GetDBVersion(conn)
	requireNoError(t, err, "version after refused rollback")
	if version != 29 {
		t.Fatalf("version after rollback=%d", version)
	}
	t.Log("PASS: rollback preserved history and version=29")
}

func assertRemoteTables(t *testing.T, conn *sql.DB) {
	t.Helper()
	for _, table := range []string{
		"agents", "agent_pairings", "agent_labels",
		"environment_agent_assignments", "step_agent_selectors",
		"step_template_agent_selectors", "step_template_version_agent_selectors",
		"deployment_step_sources", "deployment_steps",
		"deployment_step_selectors", "deployment_step_attempts",
		"deployment_dispatches", "deployment_log_scopes",
		"remote_deployment_claims", "agent_environment_labels",
	} {
		var count int
		err := conn.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count)
		requireNoError(t, err, "table "+table)
		t.Logf("table %s exists", table)
	}
}

func assertRemoteLegacy(t *testing.T, conn *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`SELECT COUNT(*) FROM releases WHERE id=1 AND version='v1'`,
		`SELECT COUNT(*) FROM deployments
		 WHERE id=1 AND release_id=1 AND environment_id=1`,
		`SELECT COUNT(*) FROM deployment_logs
		 WHERE id=7 AND deployment_id=1 AND line='legacy'`,
		`SELECT COUNT(*) FROM deployment_logs
		 WHERE id=8 AND deployment_id=1 AND line='legacy'`,
		`SELECT COUNT(*) FROM steps
		 WHERE id=1 AND execution_target='local'`,
		`SELECT COUNT(*) FROM deployment_log_scopes
		 WHERE log_id=7 AND sequence=7 AND step_index IS NULL
		 AND attempt IS NULL`,
	} {
		var count int
		requireNoError(t, conn.QueryRow(query).Scan(&count), query)
		if count != 1 {
			t.Fatalf("count=%d for %s", count, query)
		}
		t.Logf("assert count=1: %s", query)
	}
}

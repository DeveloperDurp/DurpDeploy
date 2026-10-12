package migrate

import (
	"database/sql"
	"path/filepath"
	"testing"

	"durpdeploy/migrations"
	"github.com/pressly/goose/v3"
)

func TestProjectEnvironmentQueueUpgradePreservesOwner(t *testing.T) {
	conn, err := sql.Open(
		"sqlite",
		filepath.Join(t.TempDir(), "queue.db")+"?_pragma=foreign_keys(1)",
	)
	requireNoError(t, err, "open fixture")
	defer conn.Close()
	goose.SetBaseFS(migrations.FS)
	requireNoError(t, goose.SetDialect("sqlite3"), "dialect")
	requireNoError(t, goose.UpTo(conn, ".", 53), "old queue schema")
	for _, query := range []string{
		`INSERT INTO projects(id,name) VALUES(1,'First'),(2,'Second')`,
		`INSERT INTO environments(id,name) VALUES(1,'dev')`,
		`INSERT INTO releases(id,project_id,version,steps_json)
		 VALUES(1,1,'v1','[]'),(2,2,'v1','[]')`,
		`INSERT INTO deployments(id,release_id,environment_id,status)
		 VALUES(1,1,1,'awaiting_artifact_approval'),(2,2,1,'queued')`,
		`INSERT INTO environment_deployment_slots(environment_id,deployment_id) VALUES(1,1)`,
	} {
		_, err = conn.Exec(query)
		requireNoError(t, err, "seed existing owner and queued project")
	}
	requireNoError(t, goose.Up(conn, "."), "upgrade project queue")
	var owner int64
	err = conn.QueryRow(`SELECT deployment_id FROM environment_deployment_slots
		WHERE project_id=1 AND environment_id=1`).Scan(&owner)
	requireNoError(t, err, "read preserved owner")
	if owner != 1 {
		t.Fatalf("owner=%d, want 1", owner)
	}
	_, err = conn.Exec(`INSERT INTO environment_deployment_slots
		(environment_id,project_id,deployment_id) VALUES(1,2,2)`)
	requireNoError(t, err, "admit another project in the same environment")
	var count int
	err = conn.QueryRow(`SELECT COUNT(*) FROM deployments`).Scan(&count)
	requireNoError(t, err, "read retained history")
	if count != 2 {
		t.Fatalf("deployment count=%d, want 2", count)
	}
}

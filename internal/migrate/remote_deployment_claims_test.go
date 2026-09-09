package migrate

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

func TestRemoteDeploymentClaimMigrationBaseline(t *testing.T) {
	forEachRemoteClaimDatabase(
		t,
		func(t *testing.T, fixture remoteClaimTestDB) {
			conn := fixture.openBaseline(t)
			defer conn.Close()
			fixture.seedRemoteClaimPrerequisites(t, conn, true)

			var count int
			err := conn.QueryRow(`SELECT COUNT(*)
			FROM environment_agent_assignments WHERE environment_id=2`).Scan(&count)
			requireNoError(t, err, "count baseline assignments")
			if count != 2 {
				t.Fatalf("baseline assignments=%d want=2", count)
			}
			assertRemoteClaimSchemaAbsent(t, conn)
		},
	)
}

func TestRemoteDeploymentClaimMigration(t *testing.T) {
	forEachRemoteClaimDatabase(
		t,
		func(t *testing.T, fixture remoteClaimTestDB) {
			conn := fixture.openBaseline(t)
			fixture.seedRemoteClaimPrerequisites(t, conn, false)
			requireNoError(t, conn.Close(), "close baseline connection")

			conn, err := Run(fixture.dsn)
			requireNoError(t, err, "run remote claim migration")
			defer conn.Close()

			var assigned sql.NullString
			err = conn.QueryRow(
				`SELECT assigned_agent_id FROM deployments WHERE id=1`,
			).Scan(&assigned)
			requireNoError(t, err, "read historical deployment assignment")
			if assigned.Valid {
				t.Fatalf(
					"historical deployment assignment=%q want NULL",
					assigned.String,
				)
			}
			var claims int
			requireNoError(
				t,
				conn.QueryRow(`SELECT COUNT(*) FROM remote_deployment_claims`).
					Scan(&claims),
				"count historical claims",
			)
			if claims != 0 {
				t.Fatalf("historical claims=%d want=0", claims)
			}
			if _, err := conn.Exec(`INSERT INTO environment_agent_assignments
			(environment_id,agent_id) VALUES(2,'b')`); err == nil {
				t.Fatal("second assignment for one environment accepted")
			}
			_, err = conn.Exec(`UPDATE agent_pairings SET server_pull_endpoint=?
			WHERE agent_id='a'`, "https://server.example/agent/v1/poll")
			requireNoError(t, err, "persist server pull endpoint")

			for deploymentID := int64(1); deploymentID <= 2; deploymentID++ {
				_, err = conn.Exec(`INSERT INTO remote_deployment_claims
				(deployment_id,agent_id) VALUES(?,'a')`, deploymentID)
				requireNoError(t, err, "queue deployment claim")
			}
			claimRemoteDeployment(t, conn, 1, 1)
			if _, err := claimRemoteDeploymentExec(conn, 2, 2); err == nil {
				t.Fatal("agent accepted two in-flight deployment claims")
			}
			_, err = conn.Exec(`UPDATE remote_deployment_claims SET
			state='succeeded',started_at=101,finished_at=102,updated_at=102
			WHERE deployment_id=1`)
			requireNoError(t, err, "finish first remote claim")
			claimRemoteDeployment(t, conn, 2, 2)
			if _, err := conn.Exec(`UPDATE remote_deployment_claims
			SET state='unknown' WHERE deployment_id=2`); err == nil {
				t.Fatal("unknown remote claim state accepted")
			}
		},
	)
}

func TestRemoteDeploymentClaimMigrationRejectsMultipleAssignments(
	t *testing.T,
) {
	forEachRemoteClaimDatabase(
		t,
		func(t *testing.T, fixture remoteClaimTestDB) {
			conn := fixture.openBaseline(t)
			fixture.seedRemoteClaimPrerequisites(t, conn, true)
			requireNoError(t, conn.Close(), "close ambiguous baseline")

			migrated, err := Run(fixture.dsn)
			if migrated != nil {
				migrated.Close()
				t.Fatal("ambiguous assignment migration returned a connection")
			}
			if err == nil ||
				!strings.Contains(err.Error(), "environment IDs 2") {
				t.Fatalf(
					"migration error=%v, want ambiguous environment IDs 2",
					err,
				)
			}

			conn = fixture.openRaw(t)
			defer conn.Close()
			if fixture.gooseDialect == "mssql" {
				config, configErr := migrationConfig(fixture.dsn)
				requireNoError(t, configErr, "SQL Server migration config")
				goose.SetBaseFS(config.migrationFS)
			} else {
				goose.SetBaseFS(nil)
			}
			requireNoError(
				t,
				goose.SetDialect(fixture.gooseDialect),
				"set dialect",
			)
			version, versionErr := goose.GetDBVersion(conn)
			requireNoError(t, versionErr, "read version after refusal")
			if version != fixture.baselineVersion {
				t.Fatalf("version=%d want=%d", version, fixture.baselineVersion)
			}
			assertRemoteClaimSchemaAbsent(t, conn)
		},
	)
}

func (fixture remoteClaimTestDB) seedRemoteClaimPrerequisites(
	t *testing.T,
	conn *sql.DB,
	ambiguous bool,
) {
	t.Helper()
	tx, err := conn.Begin()
	requireNoError(t, err, "begin remote claim baseline seed")
	defer tx.Rollback()
	for _, seed := range []struct {
		table string
		query string
	}{
		{"projects", `INSERT INTO projects(id,name) VALUES(1,'claims')`},
		{"environments", `INSERT INTO environments(id,name)
			VALUES(1,'unassigned'),(2,'assigned')`},
		{"releases", `INSERT INTO releases(id,project_id,version,steps_json)
		 VALUES(1,1,'v1','[]')`,
		},
		{"deployments", `INSERT INTO deployments(id,release_id,environment_id,status)
		 VALUES(1,1,2,'pending'),(2,1,2,'pending')`},
		{"", `INSERT INTO agents(id,name,endpoint) VALUES
		 ('a','a','https://a.invalid'),('b','b','https://b.invalid')`,
		},
		{"", `INSERT INTO environment_agent_assignments(environment_id,agent_id)
		 VALUES(2,'a')`},
	} {
		if fixture.gooseDialect == "mssql" && seed.table != "" {
			_, err = tx.Exec("SET IDENTITY_INSERT " + seed.table + " ON")
			requireNoError(t, err, "enable identity insert for "+seed.table)
		}
		_, err = tx.Exec(seed.query)
		requireNoError(t, err, "seed remote claim baseline")
		if fixture.gooseDialect == "mssql" && seed.table != "" {
			_, err = tx.Exec("SET IDENTITY_INSERT " + seed.table + " OFF")
			requireNoError(t, err, "disable identity insert for "+seed.table)
		}
	}
	_, err = tx.Exec(`INSERT INTO agent_pairings(agent_id,pairing_code_hash,
		agent_public_identity,agent_pin,expires_at) VALUES
		('a',?,'public',?,300)`, bytes.Repeat([]byte{1}, 32), strings.Repeat("a", 64))
	requireNoError(t, err, "seed agent pairing")
	if ambiguous {
		_, err = tx.Exec(`INSERT INTO environment_agent_assignments
			(environment_id,agent_id) VALUES(2,'b')`)
		requireNoError(t, err, "seed ambiguous assignment")
	}
	requireNoError(t, tx.Commit(), "commit remote claim baseline seed")
}

func assertRemoteClaimSchemaAbsent(t *testing.T, conn *sql.DB) {
	t.Helper()
	for _, query := range []string{
		`SELECT assigned_agent_id FROM deployments WHERE 1=0`,
		`SELECT server_pull_endpoint FROM agent_pairings WHERE 1=0`,
		`SELECT deployment_id FROM remote_deployment_claims WHERE 1=0`,
	} {
		if rows, err := conn.Query(query); err == nil {
			rows.Close()
			t.Fatalf("schema mutation exists before migration: %s", query)
		}
	}
}

func claimRemoteDeployment(
	t *testing.T,
	conn *sql.DB,
	deploymentID, token int64,
) {
	t.Helper()
	_, err := claimRemoteDeploymentExec(conn, deploymentID, token)
	requireNoError(t, err, "claim remote deployment")
}

func claimRemoteDeploymentExec(
	conn *sql.DB,
	deploymentID, token int64,
) (sql.Result, error) {
	return conn.Exec(`UPDATE remote_deployment_claims SET state='claimed',
		claim_token_hash=?,ciphertext='ciphertext',claim_expires_at=110,
		last_heartbeat_at=100,updated_at=100 WHERE deployment_id=?`,
		bytes.Repeat([]byte{byte(token)}, 32), deploymentID)
}

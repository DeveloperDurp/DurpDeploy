package migrate

import (
	"testing"
)

func TestLegacyEnvironmentAssignmentsBecomeLabels(t *testing.T) {
	forEachRemoteClaimDatabase(
		t,
		func(t *testing.T, fixture remoteClaimTestDB) {
			if fixture.gooseDialect == "mssql" {
				fixture.baselineVersion = 16
			} else {
				fixture.baselineVersion = 31
			}
			conn := fixture.openBaseline(t)
			_, err := conn.Exec(`INSERT INTO agents(id,name,endpoint)
				VALUES('legacy-agent','legacy-agent','https://agent.invalid')`)
			requireNoError(t, err, "seed legacy agent")
			_, err = conn.Exec(`INSERT INTO environments(name)
				VALUES('legacy-environment')`)
			requireNoError(t, err, "seed legacy environment")
			var environmentID int64
			err = conn.QueryRow(`SELECT id FROM environments
				WHERE name='legacy-environment'`).Scan(&environmentID)
			requireNoError(t, err, "read legacy environment")
			_, err = conn.Exec(`INSERT INTO projects(name) VALUES('legacy-project')`)
			requireNoError(t, err, "seed legacy project")
			var projectID int64
			err = conn.QueryRow(`SELECT id FROM projects
				WHERE name='legacy-project'`).Scan(&projectID)
			requireNoError(t, err, "read legacy project")
			_, err = conn.Exec(`INSERT INTO releases(project_id,version,steps_json)
				VALUES(?,'v1','[]')`, projectID)
			requireNoError(t, err, "seed legacy release")
			var releaseID int64
			err = conn.QueryRow(`SELECT id FROM releases
				WHERE project_id=? AND version='v1'`, projectID).Scan(&releaseID)
			requireNoError(t, err, "read legacy release")
			_, err = conn.Exec(`INSERT INTO environment_agent_assignments
				(environment_id,agent_id) VALUES(?,'legacy-agent')`, environmentID)
			requireNoError(t, err, "seed legacy assignment")
			_, err = conn.Exec(`INSERT INTO deployments
				(release_id,environment_id,status,assigned_agent_id)
				VALUES(?,?,'pending','legacy-agent')`, releaseID, environmentID)
			requireNoError(t, err, "seed legacy deployment")
			var deploymentID int64
			err = conn.QueryRow(`SELECT id FROM deployments
				WHERE release_id=? AND environment_id=?`,
				releaseID,
				environmentID,
			).Scan(&deploymentID)
			requireNoError(t, err, "read legacy deployment")
			_, err = conn.Exec(`INSERT INTO remote_deployment_claims
				(deployment_id,agent_id) VALUES(?,'legacy-agent')`, deploymentID)
			requireNoError(t, err, "seed legacy remote claim")
			requireNoError(t, conn.Close(), "close legacy database")

			conn, err = Run(fixture.dsn)
			requireNoError(t, err, "migrate legacy assignment")
			defer func() {
				requireNoError(t, conn.Close(), "close migrated database")
			}()

			var count int
			err = conn.QueryRow(`SELECT COUNT(*) FROM agent_environment_labels
				WHERE environment_id=? AND agent_id='legacy-agent'`,
				environmentID,
			).Scan(&count)
			requireNoError(t, err, "count migrated environment label")
			if count != 1 {
				t.Fatalf("migrated environment labels=%d want=1", count)
			}
			err = conn.QueryRow(`SELECT COUNT(*) FROM remote_deployment_claims
				WHERE deployment_id=? AND agent_id='legacy-agent'`,
				deploymentID,
			).Scan(&count)
			requireNoError(t, err, "count preserved remote claim")
			if count != 1 {
				t.Fatalf("preserved remote claims=%d want=1", count)
			}
			if rows, err := conn.Query(
				"SELECT * FROM environment_agent_assignments",
			); err == nil {
				requireNoError(t, rows.Close(), "close legacy table query")
				t.Fatal("legacy environment assignment table still exists")
			}
		},
	)
}

package migrate

import (
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/testutil"
	"github.com/pressly/goose/v3"
)

func TestDeploymentEnvironmentIdentityUpgradeAndRollback(t *testing.T) {
	forEachRemoteClaimDatabase(
		t,
		func(t *testing.T, fixture remoteClaimTestDB) {
			fixture.baselineVersion = 73
			if fixture.gooseDialect == "mssql" {
				fixture.baselineVersion = 58
			}
			conn := fixture.openBaseline(t)
			t.Cleanup(func() { testutil.Close(t, conn) })
			for _, query := range []string{
				"INSERT INTO projects(name) VALUES('identity')",
				"INSERT INTO environments(name) VALUES('dev')",
				"INSERT INTO releases(project_id,version,steps_json) VALUES(1,'identity','[]')",
				"INSERT INTO deployments(release_id,environment_id,status) VALUES(1,1,'pending')",
			} {
				_, err := conn.ExecContext(t.Context(), query)
				requireNoError(t, err, "seed legacy deployment")
			}
			requireNoError(
				t,
				goose.Up(conn, "."),
				"capture legacy environment identity",
			)
			q := db.New(conn)
			accepted, err := q.GetDeployment(t.Context(), 1)
			requireNoError(t, err, "read upgraded deployment")
			if !accepted.EnvironmentNameSnapshot.Valid ||
				accepted.EnvironmentNameSnapshot.String != "dev" {
				t.Fatal(
					"legacy deployment did not capture upgrade-time identity",
				)
			}
			_, err = conn.ExecContext(
				t.Context(),
				"UPDATE environments SET name = 'prd' WHERE id = 1",
			)
			requireNoError(t, err, "rename environment")
			accepted, err = q.GetDeployment(t.Context(), 1)
			requireNoError(t, err, "read captured identity after rename")
			if accepted.EnvironmentNameSnapshot.String != "dev" {
				t.Fatal("rename changed accepted context")
			}
			next, err := q.CreateDeployment(
				t.Context(),
				db.CreateDeploymentParams{
					ReleaseID:     1,
					EnvironmentID: 1,
					Status:        "pending",
				},
			)
			requireNoError(t, err, "accept new deployment")
			if next.EnvironmentNameSnapshot.String != "prd" {
				t.Fatal("new deployment did not capture renamed environment")
			}
			requireNoError(
				t,
				goose.DownTo(conn, ".", fixture.baselineVersion),
				"rollback identity migration",
			)
			var count int
			err = conn.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM deployments").
				Scan(&count)
			requireNoError(t, err, "read retained deployments")
			if count != 2 {
				t.Fatal("rollback lost deployment history")
			}
			requireNoError(t, goose.Up(conn, "."), "repeat identity upgrade")
		},
	)
}

package migrate

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/testutil"
	"github.com/pressly/goose/v3"
)

func TestProjectGroupsUpgradeAndRollback(t *testing.T) {
	forEachRemoteClaimDatabase(
		t,
		func(t *testing.T, fixture remoteClaimTestDB) {
			fixture.baselineVersion = 65
			if fixture.gooseDialect == "mssql" {
				fixture.baselineVersion = 50
			}
			conn := fixture.openBaseline(t)
			t.Cleanup(func() { testutil.Close(t, conn) })
			var projectID int64
			err := conn.QueryRowContext(
				t.Context(),
				"INSERT INTO projects(name) VALUES(?) RETURNING id",
				"Existing grouped project",
			).Scan(&projectID)
			requireNoError(t, err, "seed pre-group project")
			requireNoError(t, goose.Up(conn, "."), "upgrade project groups")
			q := db.New(conn)
			project, err := q.GetProject(t.Context(), projectID)
			requireNoError(t, err, "read existing project")
			if project.GroupID.Valid ||
				project.Name != "Existing grouped project" {
				t.Fatal("upgrade changed existing project")
			}
			group, err := q.CreateProjectGroup(
				t.Context(),
				db.CreateProjectGroupParams{Name: "Team", NameKey: "team"},
			)
			requireNoError(t, err, "create migrated group")
			_, err = q.SetProjectGroup(
				t.Context(),
				db.SetProjectGroupParams{
					ID:      projectID,
					GroupID: sql.NullInt64{Int64: group.ID, Valid: true},
				},
			)
			requireNoError(t, err, "assign migrated project")
			requireNoError(t, goose.Down(conn, "."), "remove project groups")
			var name string
			err = conn.QueryRowContext(t.Context(), "SELECT name FROM projects WHERE id = ?", projectID).
				Scan(&name)
			requireNoError(t, err, "read project after rollback")
			if name != project.Name {
				t.Fatal("group rollback lost project")
			}
			requireNoError(
				t,
				goose.Up(conn, "."),
				"repeat project group upgrade",
			)
			project, err = q.GetProject(t.Context(), projectID)
			requireNoError(t, err, "read re-upgraded project")
			if project.GroupID.Valid {
				t.Fatal("re-upgrade restored a removed group")
			}
		},
	)
}

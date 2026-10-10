package migrate

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestProjectVariableResetDatabaseParity(t *testing.T) {
	for _, name := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(name, func(t *testing.T) {
			conn := lifecycleVariablesParityDB(t, name)
			t.Cleanup(
				func() { requireNoError(t, conn.Close(), "close database") },
			)
			q := db.New(conn)
			project, err := q.CreateProject(
				t.Context(),
				db.CreateProjectParams{Name: "reset"},
			)
			requireNoError(t, err, "project")
			other, err := q.CreateProject(
				t.Context(),
				db.CreateProjectParams{Name: "other"},
			)
			requireNoError(t, err, "other project")
			env, err := q.CreateEnvironment(
				t.Context(),
				db.CreateEnvironmentParams{Name: "scope"},
			)
			requireNoError(t, err, "environment")
			for _, owner := range []int64{project.ID, other.ID} {
				for _, scope := range []sql.NullInt64{{}, {Int64: env.ID, Valid: true}} {
					_, err := q.CreateVariable(
						t.Context(),
						db.CreateVariableParams{
							ProjectID:     owner,
							Name:          "TOKEN",
							EnvironmentID: scope,
						},
					)
					requireNoError(t, err, "variable")
				}
			}
			// SQLite/Postgres permit legacy NULL-scope duplicates; MSSQL may not.
			if name != "SQLServer" {
				_, err := q.CreateVariable(t.Context(), db.CreateVariableParams{
					ProjectID: project.ID, Name: "TOKEN",
				})
				requireNoError(t, err, "duplicate")
			}
			requireNoError(t, q.DeleteVariableOverrides(
				t.Context(),
				db.DeleteVariableOverridesParams{
					ProjectID: project.ID,
					Name:      "TOKEN",
				},
			), "reset")
			rows, err := q.ListVariablesByProject(t.Context(), project.ID)
			requireNoError(t, err, "remaining scopes")
			if len(rows) != 1 || !rows[0].EnvironmentID.Valid {
				t.Fatal("reset left duplicates or removed scoped row", rows)
			}
			rows, err = q.ListVariablesByProject(t.Context(), other.ID)
			requireNoError(t, err, "other owner")
			if len(rows) != 2 {
				t.Fatal("reset affected another project")
			}
		})
	}
}

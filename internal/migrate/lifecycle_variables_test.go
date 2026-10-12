package migrate

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestLifecycleVariablesDatabaseParity(t *testing.T) {
	for _, name := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(name, func(t *testing.T) {
			conn := lifecycleVariablesParityDB(t, name)
			t.Cleanup(
				func() { requireNoError(t, conn.Close(), "close database") },
			)
			q := db.New(conn)
			lc, err := q.CreateLifecycle(
				t.Context(),
				db.CreateLifecycleParams{Name: "shared"},
			)
			requireNoError(t, err, "create lifecycle")
			env, err := q.CreateEnvironment(
				t.Context(),
				db.CreateEnvironmentParams{Name: "stage"},
			)
			requireNoError(t, err, "create environment")
			params := db.CreateLifecycleVariableParams{
				LifecycleID: lc.ID,
				Name:        "REGION",
				Value:       sql.NullString{String: "ciphertext", Valid: true},
			}
			global, err := q.CreateLifecycleVariable(t.Context(), params)
			requireNoError(t, err, "insert unscoped RETURNING")
			if global.ID <= 0 || global.CreatedAt <= 0 {
				t.Fatal("missing identity or creation time")
			}
			if _, err := q.CreateLifecycleVariable(t.Context(), params); err == nil {
				t.Fatal("duplicate global scope accepted")
			}
			params.EnvironmentID = sql.NullInt64{Int64: env.ID, Valid: true}
			scoped, err := q.CreateLifecycleVariable(t.Context(), params)
			requireNoError(t, err, "insert scoped RETURNING")
			if _, err := q.CreateLifecycleVariable(t.Context(), params); err == nil {
				t.Fatal("duplicate environment scope accepted")
			}
			updated, err := q.UpdateLifecycleVariableKeepValue(
				t.Context(),
				db.UpdateLifecycleVariableKeepValueParams{
					ID:            scoped.ID,
					LifecycleID:   lc.ID,
					Name:          "TOKEN",
					EnvironmentID: params.EnvironmentID,
					Secret:        1,
				},
			)
			requireNoError(t, err, "preserve value update RETURNING")
			if updated.Value.String != "ciphertext" || updated.Secret != 1 {
				t.Fatal("value update lost data")
			}
			count, err := q.DeleteLifecycleVariable(
				t.Context(),
				db.DeleteLifecycleVariableParams{
					ID:          scoped.ID,
					LifecycleID: lc.ID + 100,
				},
			)
			requireNoError(t, err, "scoped delete")
			if count != 0 {
				t.Fatal("foreign lifecycle deleted row")
			}
			requireNoError(
				t,
				q.DeleteEnvironment(t.Context(), env.ID),
				"delete environment",
			)
			rows, err := q.ListLifecycleVariables(t.Context(), lc.ID)
			requireNoError(t, err, "list after environment cascade")
			if len(rows) != 1 || rows[0].ID != global.ID {
				t.Fatal("environment cascade removed wrong scope")
			}
			requireNoError(
				t,
				q.DeleteLifecycle(t.Context(), lc.ID),
				"delete lifecycle",
			)
			rows, err = q.ListAllLifecycleVariables(t.Context())
			requireNoError(t, err, "list after lifecycle cascade")
			if len(rows) != 0 {
				t.Fatal("lifecycle cascade left variables")
			}
		})
	}
}

func lifecycleVariablesParityDB(t *testing.T, name string) *sql.DB {
	t.Helper()
	if name == "SQLServer" {
		return newSQLServerTestDB(t)
	}
	fixture := newRemoteClaimTestDB(t, name)
	conn, err := Run(fixture.dsn)
	requireNoError(t, err, "migrate lifecycle variables")
	return conn
}

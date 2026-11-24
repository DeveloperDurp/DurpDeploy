package migrate

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
)

func TestProjectVariableGroupingDatabaseParity(t *testing.T) {
	for _, backend := range []string{"SQLite", "PostgreSQL", "SQLServer"} {
		t.Run(backend, func(t *testing.T) {
			conn := lifecycleVariablesParityDB(t, backend)
			t.Cleanup(func() {
				requireNoError(t, conn.Close(), "close database")
			})
			assertProjectVariableGrouping(t, db.New(conn))
		})
	}
}

func assertProjectVariableGrouping(t *testing.T, q *db.Queries) {
	t.Helper()
	ctx := t.Context()
	project, err := q.CreateProject(ctx, db.CreateProjectParams{Name: "group"})
	requireNoError(t, err, "project")
	lower, err := q.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID: project.ID, Name: "app_url",
	})
	requireNoError(t, err, "unscoped variable with different case")
	query := db.ListVariablesByProjectPaginatedParams{
		ProjectID: project.ID, SortByName: 1, PageLimit: 1,
	}
	for _, name := range []string{"dev", "prd"} {
		env, err := q.CreateEnvironment(
			ctx,
			db.CreateEnvironmentParams{Name: name},
		)
		requireNoError(t, err, "environment")
		variable, err := q.CreateVariable(ctx, db.CreateVariableParams{
			ProjectID: project.ID, Name: "APP_URL",
			EnvironmentID: sql.NullInt64{Int64: env.ID, Valid: true},
		})
		requireNoError(t, err, "environment override")
		page, err := q.ListVariablesByProjectPaginated(ctx, query)
		requireNoError(t, err, "first scoped group")
		if len(page) != 1 || page[0].ID != variable.ID {
			t.Fatalf(
				"page %d: got %v, want %d",
				query.PageOffset,
				page,
				variable.ID,
			)
		}
		edit := db.UpdateVariableGroupValueParams{
			ID: variable.ID, ProjectID: project.ID,
			Revision: variable.RotationRevision,
			Value:    sql.NullString{String: name, Valid: true},
		}
		changed, err := q.UpdateVariableGroupValue(ctx, edit)
		requireNoError(t, err, "write grouped value")
		if changed != 1 {
			t.Fatal("expected a scoped row to be updated")
		}
		changed, err = q.UpdateVariableGroupValue(ctx, edit)
		requireNoError(t, err, "reject stale grouped value")
		if changed != 0 {
			t.Fatal("expected a stale edit to leave the row unchanged")
		}
		query.PageOffset++
	}
	page, err := q.ListVariablesByProjectPaginated(ctx, query)
	requireNoError(t, err, "separate lowercase name")
	if len(page) != 1 || page[0].ID != lower.ID {
		t.Fatalf("last page: got %v, want %d", page, lower.ID)
	}
}

package repository

import (
	"context"
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/testutil"
)

// TestPostgres_RepositoryWithTx checks transactions in a fresh PostgreSQL database.
func TestPostgres_RepositoryWithTx(t *testing.T) {
	ctx := context.Background()
	dsn := RepositoryDatabaseDSN(t, "PostgreSQL")
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer testutil.Close(t, conn)

	repo := New(conn)

	p, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{
			Name:        "repo-test",
			Description: sql.NullString{String: "desc", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Logf("created project %d %s", p.ID, p.Name)

	err = repo.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.CreateEnvironment(
			ctx,
			db.CreateEnvironmentParams{Name: "repo-test-env"},
		)
		return err
	})
	if err != nil {
		t.Fatalf("WithTx: %v", err)
	}

	envs, err := repo.Queries.ListEnvironments(ctx)
	if err != nil {
		t.Fatalf("ListEnvironments: %v", err)
	}
	t.Logf("environments: %d", len(envs))
}

package repository

import (
	"context"
	"database/sql"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

// TestPostgres_RepositoryWithTx verifies the repository (including
// transactions) works against a real PostgreSQL instance, spun up on demand
// via testcontainers-go. It is skipped automatically (via testcontainers-go's
// own Docker/Podman detection) when no container runtime is available, e.g.
// in CI.
func TestPostgres_RepositoryWithTx(t *testing.T) {
	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("could not start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := ctr.Terminate(context.Background()); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()

	repo := New(conn)

	p, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{Name: "repo-test", Description: sql.NullString{String: "desc", Valid: true}})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	t.Logf("created project %d %s", p.ID, p.Name)

	err = repo.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.CreateEnvironment(ctx, db.CreateEnvironmentParams{Name: "repo-test-env"})
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

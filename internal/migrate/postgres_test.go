package migrate

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

// TestPostgres_MigrationsRun verifies migrations apply cleanly against a
// real PostgreSQL instance, spun up on demand via testcontainers-go. It is
// skipped automatically (via testcontainers-go's own Docker/Podman
// detection) when no container runtime is available, e.g. in CI.
func TestPostgres_MigrationsRun(t *testing.T) {
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

	db, err := Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer db.Close()

	rows, err := db.Query("SELECT table_name FROM information_schema.tables WHERE table_schema='public' ORDER BY table_name")
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		t.Log(name)
	}

	// Exercise a real query using ? placeholders + unixepoch() default,
	// mirroring what sqlc-generated code does.
	var id int64
	err = db.QueryRow(`INSERT INTO projects (name, description) VALUES (?, ?) RETURNING id`, "demo", "desc").Scan(&id)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Logf("inserted project id=%d", id)

	var cnt int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE created_at >= strftime('%s','now','start of day')`).Scan(&cnt); err != nil {
		t.Fatalf("strftime query: %v", err)
	}
	t.Logf("today count=%d", cnt)
}

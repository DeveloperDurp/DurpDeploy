package migrate

import (
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"durpdeploy/internal/db"
)

// TestPostgres_InterpreterDefaultsAndConstraints mirrors
// TestSQLServer_SchemaParityDefaultsAndIndexes for the interpreter columns
// added by migrations 035/036. It is skipped automatically when no
// container runtime is available.
func TestPostgres_InterpreterDefaultsAndConstraints(t *testing.T) {
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
	dbConn, err := Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer dbConn.Close()
	queries := db.New(dbConn)

	project, err := queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "interpreter-project"},
	)
	requireNoError(t, err, "create project")
	step, err := queries.CreateStep(ctx, db.CreateStepParams{
		ProjectID: project.ID, Name: "interpreter-step", ScriptBody: "echo hi",
	})
	requireNoError(t, err, "create step without interpreter")
	if step.Interpreter != "bash" {
		t.Fatalf("step interpreter = %q, want bash", step.Interpreter)
	}
	if _, err := dbConn.Exec(
		"UPDATE steps SET interpreter = ? WHERE id = ?", "ruby", step.ID,
	); err == nil {
		t.Fatal("update step with invalid interpreter succeeded")
	}
	if _, err := dbConn.Exec(
		"UPDATE steps SET interpreter = ? WHERE id = ?", "pwsh", step.ID,
	); err != nil {
		t.Fatalf("update step with pwsh interpreter: %v", err)
	}

	if _, err := dbConn.Exec(
		"INSERT INTO agents(id, name, endpoint) VALUES(?, ?, ?)",
		"agent1", "Agent One", "https://agent1",
	); err != nil {
		t.Fatalf("create agent: %v", err)
	}
	if _, err := dbConn.Exec(
		"INSERT INTO agent_interpreters(agent_id, interpreter) VALUES(?, ?)",
		"agent1", "python3",
	); err != nil {
		t.Fatalf("insert supported interpreter: %v", err)
	}
	if _, err := dbConn.Exec(
		"INSERT INTO agent_interpreters(agent_id, interpreter) VALUES(?, ?)",
		"agent1", "/bin/sh",
	); err == nil {
		t.Fatal("insert arbitrary interpreter succeeded")
	}
	if _, err := dbConn.Exec(
		"INSERT INTO agent_interpreters(agent_id, interpreter) VALUES(?, ?)",
		"agent1", "python3",
	); err == nil {
		t.Fatal("insert duplicate agent interpreter succeeded")
	}
}

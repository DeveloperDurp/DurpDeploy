package runner

import (
	"database/sql"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestBroadcastWriterHoldsMultilineSecretPrefixAcrossWrites(t *testing.T) {
	connection, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { connection.Close() })
	repo := repository.New(connection)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "logs"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(
		t.Context(), db.CreateEnvironmentParams{Name: "logs"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "running",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	broker := NewLogBroker()
	stream := broker.Subscribe(deployment.ID)
	t.Cleanup(func() { broker.Unsubscribe(deployment.ID, stream) })
	writer := &broadcastWriter{
		broker: broker, repo: repo, deploymentID: deployment.ID,
		stepName: "secret", ctx: t.Context(),
		scrubber: NewScrubber([]string{"first\nsecond"}),
	}

	if _, err := writer.Write([]byte("prefix first\n")); err != nil {
		t.Fatal(err)
	}
	select {
	case line := <-stream:
		t.Fatalf("secret prefix was broadcast early: %q", line)
	default:
	}
	if _, err := writer.Write([]byte("second suffix\n")); err != nil {
		t.Fatal(err)
	}
	if line := <-stream; line != "prefix [REDACTED] suffix" {
		t.Fatalf("broadcast line=%q", line)
	}
	logs, err := repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(), deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 1 || logs[0].Line != "prefix [REDACTED] suffix" ||
		logs[0].StepName != (sql.NullString{String: "secret", Valid: true}) {
		t.Fatalf("stored logs=%+v", logs)
	}
}

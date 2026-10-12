package runner

import (
	"context"
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestPendingLogLimitDropsAmbiguousOutputAndKeepsState(t *testing.T) {
	// Given: output with no safe newline is retained for redaction.
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	repo := repository.New(conn)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "pending"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "pending"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "1",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Status:        "running",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(
		t.Context(),
		"INSERT INTO deployment_step_sources(deployment_id,steps_json) VALUES(?,'[]')",
		deployment.ID,
	); err != nil {
		t.Fatal(err)
	}
	attempt, cancel := context.WithCancel(t.Context())
	defer cancel()
	writer := &broadcastWriter{
		broker:        NewLogBroker(),
		repo:          repo,
		deploymentID:  deployment.ID,
		stepName:      "pending",
		ctx:           t.Context(),
		scrubber:      NewScrubber([]string{"ambiguous-secret"}),
		cancelAttempt: cancel,
	}
	if _, err := writer.Write(
		[]byte(strings.Repeat("X", maxPendingLogBytes)),
	); err != nil {
		t.Fatal(err)
	}
	// When: the next byte exceeds the pending-output budget.
	if _, err := writer.Write(
		[]byte("!"),
	); !errors.Is(
		err,
		errPendingLogLimit,
	) {
		t.Fatalf("limit error=%v", err)
	}
	// Then: the attempt cancels, Flush cannot leak output, and lifecycle state remains writable.
	if !errors.Is(attempt.Err(), context.Canceled) {
		t.Fatal("attempt was not cancelled")
	}
	writer.Flush()
	if _, err := writer.Write(
		[]byte("ambiguous-secret\n"),
	); !errors.Is(
		err,
		errPendingLogLimit,
	) {
		t.Fatal(err)
	}
	if err := writer.state("failed"); err != nil {
		t.Fatal(err)
	}
	logs, err := repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(),
		deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(logs) != 2 {
		t.Fatalf("logs=%+v", logs)
	}
	for _, log := range logs {
		if strings.Contains(log.Line, "XXXXX") ||
			strings.Contains(log.Line, "ambiguous-secret") {
			t.Fatal("pending output leaked")
		}
	}
}

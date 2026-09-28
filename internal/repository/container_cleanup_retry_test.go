package repository_test

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestDeploymentRetryBlockedUntilCleanupConfirmed(t *testing.T) {
	// Given
	repo := newTestRepo(t)
	ctx := t.Context()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "e"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID,
		Version:   "v1",
		StepsJson: `[{"name":"local","container_image":"example.com/worker:1"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	source, err := repo.Queries.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: env.ID,
		Status:        "cleanup_unconfirmed",
	})
	if err != nil {
		t.Fatal(err)
	}
	arg := db.CreateDeploymentParams{
		ReleaseID:     release.ID,
		EnvironmentID: env.ID,
		Status:        "pending",
	}

	// When
	_, err = repo.CreateDeploymentFromDeployment(ctx, arg, source.ID)

	// Then
	if !errors.Is(err, repository.ErrContainerCleanupUnconfirmed) {
		t.Fatalf("retry error=%v", err)
	}
	if _, err := repo.Queries.ConfirmContainerCleanup(
		ctx,
		sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.CreateDeploymentFromDeployment(
		ctx,
		arg,
		source.ID,
	); err != nil {
		t.Fatalf("retry after confirmation: %v", err)
	}
}

func TestRunbookRetryBlockedUntilCleanupConfirmed(t *testing.T) {
	// Given
	repo := newTestRepo(t)
	ctx := t.Context()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		ctx,
		db.CreateEnvironmentParams{Name: "e"},
	)
	if err != nil {
		t.Fatal(err)
	}
	book, version, err := repo.SaveRunbook(ctx, repository.RunbookSave{
		ProjectID: project.ID,
		Name:      "maintenance",
		StepsJSON: `[{"name":"local","container_image":"example.com/worker:1"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, _, err := repo.CreateRunbookExecution(
		ctx,
		repository.RunbookExecutionRequest{
			ProjectID: project.ID, RunbookID: book.ID,
			VersionID: version.ID, EnvironmentID: env.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID: execution.DeploymentID, Status: "cleanup_unconfirmed",
		},
	); err != nil {
		t.Fatal(err)
	}
	arg := repository.RunbookExecutionRequest{
		ProjectID: project.ID, RunbookID: book.ID,
		VersionID: version.ID, EnvironmentID: env.ID,
		RetrySourceDeploymentID: execution.DeploymentID,
	}

	// When
	_, _, err = repo.CreateRunbookExecution(ctx, arg)

	// Then
	if !errors.Is(err, repository.ErrContainerCleanupUnconfirmed) {
		t.Fatalf("retry error=%v", err)
	}
	if _, err := repo.Queries.ConfirmContainerCleanup(
		ctx,
		sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := repo.CreateRunbookExecution(ctx, arg); err != nil {
		t.Fatalf("retry after confirmation: %v", err)
	}
}

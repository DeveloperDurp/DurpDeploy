package repository_test

import (
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestOtherNamespaceSweepKeepsRunbookExecutionBlocked(t *testing.T) {
	// Given: a scheduled runbook has an unconfirmed local container.
	repo := newTestRepo(t)
	ctx := t.Context()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "e"})
	if err != nil {
		t.Fatal(err)
	}
	book, version, err := repo.SaveRunbook(ctx, repository.RunbookSave{
		ProjectID: project.ID, Name: "maintenance",
		StepsJSON: `[{"name":"check","container_image":"alpine:3"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := repo.Queries.CreateRunbookSchedule(
		ctx,
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, EnvironmentID: environment.ID,
			Cron: "* * * * *", NextRunAt: 100,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	execution, _, err := repo.CreateRunbookExecution(ctx,
		repository.RunbookExecutionRequest{
			ProjectID:     project.ID,
			RunbookID:     book.ID,
			VersionID:     version.ID,
			EnvironmentID: environment.ID,
			ScheduleID: sql.NullInt64{
				Int64: schedule.ID,
				Valid: true,
			},
			ScheduleExpectedRunAt: 100,
			ScheduleNextRunAt:     160,
			FiredAt:               100,
		})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID: execution.DeploymentID, Status: "running",
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.RecordContainerNamespace(ctx,
		db.RecordContainerNamespaceParams{
			DeploymentID: execution.DeploymentID,
			Namespace:    sql.NullString{String: "original", Valid: true},
		}); err != nil {
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

	// When: the current namespace is swept successfully instead.
	confirmed, err := repo.Queries.ConfirmContainerCleanup(ctx,
		db.ConfirmContainerCleanupParams{
			Now:       sql.NullInt64{Int64: 200, Valid: true},
			Namespace: sql.NullString{String: "replacement", Valid: true},
		})
	if err != nil || confirmed != 0 {
		t.Fatalf("wrong namespace confirmed %d records: %v", confirmed, err)
	}

	// Then: no duplicate scheduled run or deletion can erase the evidence.
	_, _, err = repo.CreateRunbookExecution(ctx,
		repository.RunbookExecutionRequest{
			ProjectID:     project.ID,
			RunbookID:     book.ID,
			VersionID:     version.ID,
			EnvironmentID: environment.ID,
			ScheduleID: sql.NullInt64{
				Int64: schedule.ID,
				Valid: true,
			},
			ScheduleExpectedRunAt: 160,
			ScheduleNextRunAt:     220,
			FiredAt:               160,
		})
	if !errors.Is(err, repository.ErrRunbookScheduleOverlap) {
		t.Fatalf("schedule overlap = %v", err)
	}
	count, err := repo.Queries.CountRunbookExecutions(ctx, project.ID)
	if err != nil || count != 1 {
		t.Fatalf("executions after overlap = %d: %v", count, err)
	}
	if err := repo.DeleteProject(ctx, project.ID); !errors.Is(err,
		repository.ErrProjectHasActiveRunbook) {
		t.Fatalf("project deletion = %v", err)
	}
	if err := repo.DeleteEnvironment(ctx, environment.ID); !errors.Is(err,
		repository.ErrEnvironmentHasActiveDeployment) {
		t.Fatalf("environment deletion = %v", err)
	}
}

func TestProjectDeletionBlockedByUnconfirmedDeployment(t *testing.T) {
	// Given: a regular deployment still has unconfirmed container cleanup.
	repo := newTestRepo(t)
	ctx := t.Context()
	project, err := repo.Queries.CreateProject(
		ctx,
		db.CreateProjectParams{Name: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	environment, err := repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "e"})
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
		ProjectID: project.ID, Version: "v1", StepsJson: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		ctx,
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: environment.ID,
			Status: "cleanup_unconfirmed",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	// When: someone tries to delete its project.
	err = repo.DeleteProject(ctx, project.ID)

	// Then: the deployment remains available for reconciliation.
	if !errors.Is(err, repository.ErrProjectHasActiveRunbook) {
		t.Fatalf("project deletion = %v", err)
	}
	stored, err := repo.Queries.GetDeployment(ctx, deployment.ID)
	if err != nil || stored.Status != "cleanup_unconfirmed" {
		t.Fatalf("unconfirmed deployment = %+v: %v", stored, err)
	}
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/testutil"
)

type runbookDeletionOperation struct {
	name string
	run  func(context.Context) error
}

func TestRunbookDeletionAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		first, second := openDeploymentCreationEngine(
			t, newDeploymentCreationEngine(t, name),
		)
		checkRunbookDeletion(t, first, second)
	})
}

func checkRunbookDeletion(t *testing.T, first, second *Repository) {
	t.Helper()
	book, version, err := first.SaveRunbook(t.Context(), RunbookSave{
		ProjectID: 1, Name: "delete", StepsJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	arg := db.GetRunbookParams{ID: book.ID, ProjectID: 1}
	schedule, err := first.CreateRunbookSchedule(t.Context(), 1,
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, EnvironmentID: 1, Cron: "* * * * *",
			NextRunAt: 1,
		})
	if err != nil {
		t.Fatal(err)
	}
	request := RunbookExecutionRequest{
		ProjectID: 1, RunbookID: book.ID, EnvironmentID: 1,
	}
	execution, result, err := first.CreateRunbookExecution(
		t.Context(),
		request,
	)
	if err != nil {
		t.Fatal(err)
	}

	assertRunbookDeletionBlocked(
		t, first, second, arg, result.Deployment.ID, schedule.ID,
	)
	if err := first.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{ID: result.Deployment.ID, Status: "succeeded"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Queries.CreateDeploymentLog(t.Context(),
		db.CreateDeploymentLogParams{DeploymentID: result.Deployment.ID, Line: "retained"},
	); err != nil {
		t.Fatal(err)
	}
	request.RetrySourceDeploymentID = execution.DeploymentID
	operations := []runbookDeletionOperation{
		{"execute", func(ctx context.Context) error {
			_, _, err := second.CreateRunbookExecution(
				ctx,
				RunbookExecutionRequest{
					ProjectID:     1,
					RunbookID:     book.ID,
					EnvironmentID: 1,
				},
			)
			return err
		}},
		{"retry", func(ctx context.Context) error {
			_, _, err := second.CreateRunbookExecution(ctx, request)
			return err
		}},
		{"schedule", func(ctx context.Context) error {
			_, err := second.CreateRunbookSchedule(
				ctx,
				1,
				db.CreateRunbookScheduleParams{
					RunbookID: book.ID, EnvironmentID: 1, Cron: "* * * * *", NextRunAt: 1,
				},
			)
			return err
		}},
		{"save", func(ctx context.Context) error {
			_, _, err := second.SaveRunbook(ctx, RunbookSave{
				ProjectID: 1, RunbookID: book.ID, StepsJSON: "[]",
			})
			return err
		}},
		{"stale scheduler claim", func(ctx context.Context) error {
			_, _, err := second.CreateRunbookExecution(
				ctx,
				RunbookExecutionRequest{
					ProjectID: 1, RunbookID: book.ID, EnvironmentID: 1,
					ScheduleID: sql.NullInt64{
						Int64: schedule.ID,
						Valid: true,
					},
					ScheduleExpectedRunAt: 1, ScheduleNextRunAt: 2, FiredAt: 1,
				},
			)
			return err
		}},
	}

	assertRunbookDeletionAdmission(t, first, second, arg, operations)
	assertRunbookDeletionHistory(t, first, book, version, execution)
}

func assertRunbookDeletionAdmission(
	t *testing.T, first, second *Repository,
	arg db.GetRunbookParams, operations []runbookDeletionOperation,
) {
	t.Helper()
	// Given: deletion owns the project lock. When: admission competes.
	// Then: admission waits, and every path rejects the committed tombstone.
	tx, err := first.DB.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer testutil.Rollback(t, tx)
	if _, err := deleteRunbook(t.Context(), first.Queries.WithTx(tx), arg); err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		blocked, cancel := context.WithTimeout(
			t.Context(),
			150*time.Millisecond,
		)
		err := operation.run(blocked)
		cancel()
		if err == nil || blocked.Err() != context.DeadlineExceeded {
			t.Fatalf(
				"%s did not wait for deletion: %v",
				operation.name,
				err,
			)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	for _, operation := range operations {
		if err := operation.run(t.Context()); !errors.Is(
			err,
			sql.ErrNoRows,
		) {
			t.Fatalf("%s after deletion=%v", operation.name, err)
		}
	}
	if _, err := second.DeleteRunbook(t.Context(), arg); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("repeated deletion=%v", err)
	}
}

func assertRunbookDeletionHistory(
	t *testing.T, repo *Repository,
	book db.Runbook, version db.RunbookVersion, execution db.RunbookExecution,
) {
	t.Helper()
	if _, err := repo.Queries.GetRunbookVersion(t.Context(),
		db.GetRunbookVersionParams{ID: version.ID, RunbookID: book.ID}); err != nil {
		t.Fatal(err)
	}
	history, err := repo.Queries.GetRunbookExecution(t.Context(),
		db.GetRunbookExecutionParams{ID: execution.ID, ProjectID: 1})
	if err != nil || !history.RunbookDeletedAt.Valid ||
		history.RunbookName != book.Name {
		t.Fatalf("retained history=%+v error=%v", history, err)
	}
	logs, err := repo.ListRunbookLogs(t.Context(), execution.DeploymentID)
	if err != nil || len(logs) != 1 || logs[0].Line != "retained" {
		t.Fatalf("retained logs=%+v error=%v", logs, err)
	}
	due, err := repo.Queries.ListDueRunbookSchedules(t.Context(), 10)
	if err != nil || len(due) != 0 {
		t.Fatalf("deleted schedules still due=%+v error=%v", due, err)
	}
	if _, _, err := repo.SaveRunbook(t.Context(), RunbookSave{
		ProjectID: 1, Name: book.Name, StepsJSON: "[]",
	}); err == nil {
		t.Fatal("deleted name was reused")
	}
}

func assertRunbookDeletionBlocked(
	t *testing.T, first, second *Repository,
	arg db.GetRunbookParams, deploymentID, scheduleID int64,
) {
	t.Helper()
	// Given: an admitted execution. When: deletion competes with it.
	// Then: each unfinished state blocks deletion without disabling schedules.
	for _, state := range []string{
		"queued", "pending", "running", "pending_approval",
		"publishing_artifact", "awaiting_artifact_approval", "cleanup_unconfirmed",
	} {
		t.Run(state, func(t *testing.T) {
			if err := first.Queries.UpdateDeploymentStatus(t.Context(),
				db.UpdateDeploymentStatusParams{ID: deploymentID, Status: state},
			); err != nil {
				t.Fatal(err)
			}
			if _, err := second.DeleteRunbook(t.Context(), arg); !errors.Is(
				err, ErrRunbookHasActiveExecution,
			) {
				t.Fatalf("delete unfinished execution=%v", err)
			}
		})
	}
	stored, err := first.Queries.GetRunbookSchedule(t.Context(),
		db.GetRunbookScheduleParams{ID: scheduleID, RunbookID: arg.ID})
	if err != nil || stored.Enabled != 1 {
		t.Fatalf("blocked deletion changed schedule=%+v error=%v", stored, err)
	}
}

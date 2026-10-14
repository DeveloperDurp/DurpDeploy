package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

var ErrRunbookHasActiveExecution = errors.New(
	"runbook has unfinished executions or unconfirmed cleanup. " +
		"Finish or cancel executions and confirm cleanup before deleting",
)

func (r *Repository) DeleteRunbook(
	ctx context.Context,
	arg db.GetRunbookParams,
) (db.Runbook, error) {
	var book db.Runbook
	err := withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			var err error
			book, err = deleteRunbook(ctx, q, arg)
			return err
		})
	})
	if err != nil {
		return db.Runbook{}, fmt.Errorf("delete runbook: %w", err)
	}
	return book, nil
}

func deleteRunbook(
	ctx context.Context,
	q *db.Queries,
	arg db.GetRunbookParams,
) (db.Runbook, error) {
	if _, err := q.LockProject(ctx, arg.ProjectID); err != nil {
		return db.Runbook{}, err
	}
	book, err := q.GetRunbook(ctx, arg)
	if err != nil {
		return db.Runbook{}, err
	}
	versions, err := q.ListRunbookVersions(ctx, book.ID)
	if err != nil {
		return db.Runbook{}, err
	}
	for _, version := range versions {
		active, err := q.HasActiveReleaseDeployment(ctx, version.ReleaseID)
		if err != nil {
			return db.Runbook{}, err
		}
		if active != 0 {
			return db.Runbook{}, ErrRunbookHasActiveExecution
		}
	}
	if err := q.DisableRunbookSchedules(ctx, book.ID); err != nil {
		return db.Runbook{}, err
	}
	if err := q.MarkRunbookDeleted(ctx, db.MarkRunbookDeletedParams{
		ID: book.ID, ProjectID: book.ProjectID,
	}); err != nil {
		return db.Runbook{}, err
	}
	return book, nil
}

func (r *Repository) CreateRunbookSchedule(
	ctx context.Context,
	projectID int64,
	arg db.CreateRunbookScheduleParams,
) (db.RunbookSchedule, error) {
	var schedule db.RunbookSchedule
	err := withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if _, err := q.LockDeploymentEnvironment(ctx, arg.EnvironmentID); err != nil {
				return err
			}
			if _, err := q.LockProject(ctx, projectID); err != nil {
				return err
			}
			if _, err := q.GetRunbook(ctx, db.GetRunbookParams{
				ID: arg.RunbookID, ProjectID: projectID,
			}); err != nil {
				return err
			}
			if arg.VersionID.Valid {
				if _, err := q.GetRunbookVersion(ctx, db.GetRunbookVersionParams{
					ID: arg.VersionID.Int64, RunbookID: arg.RunbookID,
				}); err != nil {
					return err
				}
			}
			var err error
			schedule, err = q.CreateRunbookSchedule(ctx, arg)
			return err
		})
	})
	if err != nil {
		return db.RunbookSchedule{}, fmt.Errorf(
			"create runbook schedule: %w",
			err,
		)
	}
	return schedule, nil
}

// lockRunbookAdmission uses the deployment lock order before reading the book.
func lockRunbookAdmission(
	ctx context.Context,
	q *db.Queries,
	arg RunbookExecutionRequest,
) error {
	locked, err := q.LockDeploymentEnvironment(ctx, arg.EnvironmentID)
	if err != nil {
		return err
	}
	if locked == 0 {
		return sql.ErrNoRows
	}
	if _, err := q.LockProject(ctx, arg.ProjectID); err != nil {
		return err
	}
	_, err = q.GetRunbook(ctx, db.GetRunbookParams{
		ID: arg.RunbookID, ProjectID: arg.ProjectID,
	})
	return err
}

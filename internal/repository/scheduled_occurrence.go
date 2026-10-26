package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

var ErrScheduleOccurrenceChanged = errors.New("schedule occurrence changed")

type ScheduledOccurrence struct {
	Schedule  db.ScheduledDeployment
	NextRunAt int64
	FiredAt   int64
}

// UpdateScheduledOccurrence changes only the cursor, firing, and failure state.
// An edited or deleted observation is a conflict, just as on successful firing.
func (r *Repository) UpdateScheduledOccurrence(
	ctx context.Context,
	observed, updated db.ScheduledDeployment,
) error {
	return updateScheduledOccurrence(ctx, r.Queries, observed, updated)
}

func updateScheduledOccurrence(
	ctx context.Context,
	q *db.Queries,
	observed, updated db.ScheduledDeployment,
) error {
	changed, err := q.AdvanceScheduledDeploymentOccurrence(
		ctx,
		db.AdvanceScheduledDeploymentOccurrenceParams{
			NextRunAt: updated.NextRunAt, FiredAt: updated.LastFiredAt,
			Enabled: updated.Enabled, LastError: updated.LastError,
			ID: observed.ID, ObservedRunAt: observed.NextRunAt,
			ProjectID: observed.ProjectID, ReleaseID: observed.ReleaseID,
			EnvironmentID: observed.EnvironmentID,
			Cron:          observed.Cron, Note: observed.Note,
		},
	)
	if err != nil {
		return fmt.Errorf("update schedule occurrence: %w", err)
	}
	if changed != 1 {
		return ErrScheduleOccurrenceChanged
	}
	return nil
}

// CreateScheduledOccurrence advances the observed schedule in the same queue
// transaction as deployment creation. A stale or edited occurrence creates none.
func (r *Repository) CreateScheduledOccurrence(
	ctx context.Context,
	arg db.CreateDeploymentParams,
	occurrence ScheduledOccurrence,
) (DeploymentResult, error) {
	if err := r.MaintainArtifactGates(ctx); err != nil {
		return DeploymentResult{}, err
	}
	var result DeploymentResult
	err := withSQLiteBusyRetry(ctx, func() error {
		result = DeploymentResult{}
		return r.withQueueTx(
			ctx,
			func(ctx context.Context, q *db.Queries) error {
				updated := occurrence.Schedule
				updated.NextRunAt = occurrence.NextRunAt
				updated.LastFiredAt = sql.NullInt64{
					Int64: occurrence.FiredAt, Valid: true,
				}
				if err := updateScheduledOccurrence(
					ctx, q, occurrence.Schedule, updated,
				); err != nil {
					return err
				}
				var err error
				result, err = r.createDeployment(ctx, q, arg)
				return err
			},
		)
	})
	if err != nil {
		return DeploymentResult{}, fmt.Errorf(
			"create scheduled occurrence: %w",
			err,
		)
	}
	return result, nil
}

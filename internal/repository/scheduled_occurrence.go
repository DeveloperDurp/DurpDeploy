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
				schedule := occurrence.Schedule
				changed, err := q.AdvanceScheduledDeploymentOccurrence(
					ctx,
					db.AdvanceScheduledDeploymentOccurrenceParams{
						NextRunAt: occurrence.NextRunAt,
						FiredAt: sql.NullInt64{
							Int64: occurrence.FiredAt,
							Valid: true,
						},
						ID:            schedule.ID,
						ObservedRunAt: schedule.NextRunAt,
						ReleaseID:     schedule.ReleaseID,
						EnvironmentID: schedule.EnvironmentID,
						Cron:          schedule.Cron,
						Note:          schedule.Note,
					},
				)
				if err != nil {
					return fmt.Errorf("advance schedule occurrence: %w", err)
				}
				if changed != 1 {
					return ErrScheduleOccurrenceChanged
				}
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

package repository

import (
	"context"

	"durpdeploy/internal/db"
)

func (r *Repository) CreateScheduledDeployment(
	ctx context.Context,
	arg db.CreateScheduledDeploymentParams,
) (db.ScheduledDeployment, error) {
	var schedule db.ScheduledDeployment
	err := withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if _, err := q.LockDeploymentEnvironment(ctx, arg.EnvironmentID); err != nil {
				return err
			}
			if err := lockRelease(ctx, q, arg.ReleaseID); err != nil {
				return err
			}
			var err error
			schedule, err = q.CreateScheduledDeployment(ctx, arg)
			return err
		})
	})
	if err != nil {
		return db.ScheduledDeployment{}, err
	}
	return schedule, nil
}

func (r *Repository) UpdateScheduledDeployment(
	ctx context.Context,
	arg db.UpdateScheduledDeploymentParams,
) (db.ScheduledDeployment, error) {
	var schedule db.ScheduledDeployment
	err := withSQLiteBusyRetry(ctx, func() error {
		return r.WithTx(ctx, func(q *db.Queries) error {
			if _, err := q.LockDeploymentEnvironment(ctx, arg.EnvironmentID); err != nil {
				return err
			}
			if err := lockRelease(ctx, q, arg.ReleaseID); err != nil {
				return err
			}
			var err error
			schedule, err = q.UpdateScheduledDeployment(ctx, arg)
			return err
		})
	})
	if err != nil {
		return db.ScheduledDeployment{}, err
	}
	return schedule, nil
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

var ErrDeploymentQueueConflict = errors.New("deployment queue state changed")

const QueuedCancellationLogLine = "Not run: cancelled while queued. " +
	"No output exists because this step never started."

// Batch maintenance already changes many environments; lock them in ID order.
// Execution admission itself only locks its own environment.
func (r *Repository) WithQueueMaintenanceTx(
	ctx context.Context, fn func(context.Context, *db.Queries) error,
) error {
	return withSQLiteBusyRetry(ctx, func() error {
		return r.withQueueTx(
			ctx,
			func(ctx context.Context, q *db.Queries) error {
				environments, err := q.ListDeploymentQueueEnvironments(ctx)
				if err != nil {
					return err
				}
				for _, environmentID := range environments {
					if _, err := q.LockDeploymentEnvironment(
						ctx,
						environmentID,
					); err != nil {
						return err
					}
				}
				if err := fn(ctx, q); err != nil {
					return err
				}
				for _, environmentID := range environments {
					if err := advanceEnvironmentQueue(
						ctx,
						q,
						environmentID,
					); err != nil {
						return err
					}
				}
				return nil
			},
		)
	})
}

// WithDeploymentTx serializes lifecycle changes with admission and cancellation.
func (r *Repository) WithDeploymentTx(
	ctx context.Context, id int64, fn func(context.Context, *db.Queries) error,
) error {
	return withSQLiteBusyRetry(ctx, func() error {
		d, err := r.Queries.GetDeployment(ctx, id)
		if err != nil {
			return err
		}
		return r.withQueueTx(
			ctx,
			func(ctx context.Context, q *db.Queries) error {
				if _, err := q.LockDeploymentEnvironment(
					ctx,
					d.EnvironmentID,
				); err != nil {
					return err
				}
				if err := fn(ctx, q); err != nil {
					return err
				}
				return advanceEnvironmentQueue(ctx, q, d.EnvironmentID)
			},
		)
	})
}

func advanceEnvironmentQueue(
	ctx context.Context, q *db.Queries, environmentID int64,
) error {
	projects, err := q.ListEnvironmentQueueProjects(ctx, environmentID)
	if err != nil {
		return err
	}
	for _, deploymentID := range projects {
		if err := advanceDeploymentQueue(ctx, q, deploymentID); err != nil {
			return err
		}
	}
	return nil
}

func advanceDeploymentQueue(
	ctx context.Context, q *db.Queries, deploymentID int64,
) error {
	d, err := q.GetDeployment(ctx, deploymentID)
	if err != nil {
		return err
	}
	blockers, err := q.ListDeploymentQueueBlockers(ctx, deploymentID)
	if err != nil {
		return fmt.Errorf("read environment blockers: %w", err)
	}
	owner, err := q.GetDeploymentSlot(ctx, deploymentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if len(blockers) > 0 {
		if errors.Is(err, sql.ErrNoRows) {
			return q.CreateEnvironmentDeploymentSlot(ctx,
				db.CreateEnvironmentDeploymentSlotParams{
					EnvironmentID: d.EnvironmentID, DeploymentID: blockers[0],
				})
		}
		return nil
	}
	if err == nil {
		if err := q.DeleteDeploymentSlot(
			ctx,
			owner,
		); err != nil {
			return err
		}
		queueAudit(ctx, owner, "deployment_queue_released")
	}
	next, err := q.GetNextQueuedDeployment(ctx, deploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := q.CreateEnvironmentDeploymentSlot(ctx,
		db.CreateEnvironmentDeploymentSlotParams{
			EnvironmentID: d.EnvironmentID, DeploymentID: next,
		}); err != nil {
		return err
	}
	if _, err := q.AdmitQueuedDeployment(ctx, next); err != nil {
		return err
	}
	queueAudit(ctx, next, "deployment_queue_admitted")
	return nil
}

func (r *Repository) ReconcileDeploymentQueues(ctx context.Context) error {
	environments, err := r.Queries.ListDeploymentQueueEnvironments(ctx)
	if err != nil {
		return err
	}
	for _, environmentID := range environments {
		err := withSQLiteBusyRetry(ctx, func() error {
			return r.withQueueTx(
				ctx,
				func(ctx context.Context, q *db.Queries) error {
					if _, err := q.LockDeploymentEnvironment(
						ctx,
						environmentID,
					); err != nil {
						return err
					}
					return advanceEnvironmentQueue(ctx, q, environmentID)
				},
			)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) StartLocalDeployment(
	ctx context.Context,
	id int64,
) (bool, error) {
	var started bool
	err := r.WithDeploymentTx(
		ctx,
		id,
		func(ctx context.Context, q *db.Queries) error {
			var err error
			started, err = startLocalDeployment(ctx, q, id)
			return err
		},
	)
	return started, err
}

func startLocalDeployment(
	ctx context.Context, q *db.Queries, id int64,
) (bool, error) {
	d, err := q.GetDeployment(ctx, id)
	if err != nil {
		return false, err
	}
	if err := advanceEnvironmentQueue(
		ctx,
		q,
		d.EnvironmentID,
	); err != nil {
		return false, err
	}
	blockers, err := q.ListDeploymentQueueBlockers(
		ctx,
		d.ID,
	)
	if err != nil {
		return false, err
	}
	for _, blocker := range blockers {
		if blocker != id {
			return false, nil
		}
	}
	changed, err := q.StartQueuedLocalDeployment(ctx, id)
	return changed == 1, err
}

func (r *Repository) CancelQueuedDeployment(
	ctx context.Context,
	id int64,
) error {
	return r.WithDeploymentTx(
		ctx,
		id,
		func(ctx context.Context, q *db.Queries) error {
			deployment, err := q.GetDeployment(ctx, id)
			if err != nil {
				return err
			}
			changed, err := q.CancelQueuedDeployment(ctx, id)
			if err != nil {
				return err
			}
			if changed != 1 {
				return ErrDeploymentQueueConflict
			}
			if deployment.Status == "queued" {
				if err := recordQueuedCancellation(ctx, q, id); err != nil {
					return err
				}
			}
			if err := q.CancelWaitingQueuedRemoteClaim(ctx, id); err != nil {
				return err
			}
			if err := q.FinishDeploymentVerification(ctx,
				db.FinishDeploymentVerificationParams{
					DeploymentID: id, Status: "cancelled",
				}); err != nil {
				return err
			}
			queueAudit(ctx, id, "deployment_queue_cancelled")
			return nil
		},
	)
}

func recordQueuedCancellation(
	ctx context.Context, q *db.Queries, id int64,
) error {
	locked, err := q.LockDeploymentLogStream(ctx, id)
	if err != nil {
		return err
	}
	if locked != 1 {
		return sql.ErrNoRows
	}
	steps, err := q.ListDeploymentSteps(ctx, id)
	if err != nil {
		return err
	}
	for _, step := range steps {
		if _, err := q.CreateStepDeploymentLog(ctx,
			db.CreateStepDeploymentLogParams{
				DeploymentID: id, Line: QueuedCancellationLogLine,
				StepIndex: sql.NullInt64{Int64: step.StepIndex, Valid: true},
				StepName:  sql.NullString{String: step.Name, Valid: true},
				StepState: sql.NullString{String: "cancelled", Valid: true},
			}); err != nil {
			return err
		}
	}
	return nil
}

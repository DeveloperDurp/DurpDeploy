package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"durpdeploy/internal/db"
)

var ErrDeploymentQueueConflict = errors.New("deployment queue state changed")

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

type queueAuditKey struct{}

// Audit after commit so an unavailable audit table cannot undo execution state.
func (r *Repository) withQueueTx(
	ctx context.Context, fn func(context.Context, *db.Queries) error,
) error {
	var entries []db.CreateAuditLogParams
	ctx = context.WithValue(ctx, queueAuditKey{}, &entries)
	if err := r.WithTx(ctx, func(q *db.Queries) error {
		return fn(ctx, q)
	}); err != nil {
		return err
	}
	for _, entry := range entries {
		if _, err := r.Queries.CreateAuditLog(ctx, entry); err != nil {
			slog.Warn("queue audit insert failed", "action", entry.Action,
				"err", err)
		}
	}
	return nil
}

func queueAudit(ctx context.Context, id int64, action string) {
	entries := ctx.Value(queueAuditKey{}).(*[]db.CreateAuditLogParams)
	*entries = append(*entries, db.CreateAuditLogParams{
		Action: action, EntityType: "deployment",
		EntityID: sql.NullInt64{Int64: id, Valid: true},
	})
}

// ReconcileDeploymentQueues repairs missed wakeups; the database is the queue.
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
			changed, err := q.CancelQueuedDeployment(ctx, id)
			if err != nil {
				return err
			}
			if changed != 1 {
				return ErrDeploymentQueueConflict
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

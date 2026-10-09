package repository

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"
	"durpdeploy/internal/logscrub"
)

// Serialize log allocation through commit per deployment. Identity values alone
// do not follow commit order on PostgreSQL. The frozen source provides a durable
// row that cancellation and agent lifecycle transitions do not need to lock.
func (r *Repository) withDeploymentLogTx(
	ctx context.Context,
	id int64,
	fn func(*db.Queries) error,
) error {
	return r.WithTx(ctx, func(q *db.Queries) error {
		locked, err := q.LockDeploymentLogStream(ctx, id)
		if err != nil {
			return err
		}
		if locked != 1 {
			return sql.ErrNoRows
		}
		return fn(q)
	})
}

// Commit the terminal state and buffered tail together, before a reader can
// observe completion. The same lock also orders IDs across concurrent agents.
func (r *Repository) FinishRemoteStepWithLogs(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	state string,
	scrubber *logscrub.Scrubber,
) ([]db.DeploymentLog, bool, error) {
	var inserted []db.DeploymentLog
	handled := false
	err := r.WithDeploymentTx(
		ctx,
		identity.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
			locked, err := q.LockDeploymentLogStream(ctx, identity.DeploymentID)
			if err != nil {
				return err
			}
			if locked != 1 {
				return sql.ErrNoRows
			}
			if state == "cancelled" {
				_, handled, err = acknowledgeRemoteStepCancellation(
					ctx,
					q,
					identity,
				)
			} else {
				handled, err = finishRemoteStep(ctx, q, identity, state)
			}
			if err != nil || !handled {
				return err
			}
			run, err := remoteStepRun(ctx, q, identity)
			if err != nil {
				return err
			}
			inserted, err = r.appendRemoteStepLogs(
				ctx,
				q,
				identity,
				run,
				nil,
				scrubber,
				true,
			)
			return err
		},
	)
	return inserted, handled, err
}

func (r *Repository) FinishRemoteDeploymentWithLogs(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	state string,
	scrubber *logscrub.Scrubber,
) ([]db.DeploymentLog, RemoteTerminalResult, error) {
	var inserted []db.DeploymentLog
	var result RemoteTerminalResult
	err := r.WithDeploymentTx(
		ctx,
		identity.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
			locked, err := q.LockDeploymentLogStream(ctx, identity.DeploymentID)
			if err != nil {
				return err
			}
			if locked != 1 {
				return sql.ErrNoRows
			}
			if state == "cancelled" {
				_, err = acknowledgeRemoteCancellation(ctx, q, identity)
			} else {
				result, err = finishRemoteDeploymentLifecycle(
					ctx,
					q,
					identity,
					state,
				)
			}
			if err != nil {
				return err
			}
			inserted, err = r.appendRemoteDeploymentLogs(
				ctx,
				q,
				identity,
				nil,
				scrubber,
				true,
			)
			return err
		},
	)
	return inserted, result, err
}

func (r *Repository) CreateStepDeploymentLog(
	ctx context.Context,
	arg db.CreateStepDeploymentLogParams,
) (db.DeploymentLog, error) {
	var log db.DeploymentLog
	err := r.withDeploymentLogTx(
		ctx,
		arg.DeploymentID,
		func(q *db.Queries) error {
			var err error
			log, err = q.CreateStepDeploymentLog(ctx, arg)
			return err
		},
	)
	return log, err
}

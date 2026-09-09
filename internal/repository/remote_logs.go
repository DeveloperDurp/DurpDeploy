package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

type RemoteLogEvent struct {
	Sequence int64
	Line     string
}

func (r *Repository) AppendRemoteDeploymentLogs(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	events []RemoteLogEvent,
) ([]db.DeploymentLog, error) {
	inserted := make([]db.DeploymentLog, 0, len(events))
	err := r.WithTx(ctx, func(q *db.Queries) error {
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		claim, deployment, err := lockRemoteLifecycle(
			ctx, q, identity, true,
		)
		if err != nil {
			return err
		}
		if deployment.Status != "running" ||
			(claim.State != "started" && claim.State != "cancel_requested") {
			return ErrRemoteLifecycleConflict
		}
		for _, event := range events {
			if event.Sequence < 0 {
				return ErrInvalidRemoteLog
			}
			log, created, err := appendRemoteDeploymentLog(
				ctx, q, identity.DeploymentID, now, event,
			)
			if err != nil {
				return err
			}
			if created {
				inserted = append(inserted, log)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return inserted, nil
}

func appendRemoteDeploymentLog(
	ctx context.Context,
	q *db.Queries,
	deploymentID int64,
	now int64,
	event RemoteLogEvent,
) (db.DeploymentLog, bool, error) {
	log, err := q.GetRemoteDeploymentLogBySequence(
		ctx,
		db.GetRemoteDeploymentLogBySequenceParams{
			DeploymentID: deploymentID,
			Sequence:     event.Sequence,
		},
	)
	if err == nil {
		return log, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return db.DeploymentLog{}, false, err
	}
	log, err = q.CreateRemoteDeploymentLog(
		ctx,
		db.CreateRemoteDeploymentLogParams{
			DeploymentID: deploymentID,
			Line:         event.Line,
			CreatedAt:    now,
		},
	)
	if err != nil {
		return db.DeploymentLog{}, false, err
	}
	err = q.CreateDeploymentLogScope(
		ctx,
		db.CreateDeploymentLogScopeParams{
			LogID:        log.ID,
			DeploymentID: deploymentID,
			Sequence:     event.Sequence,
		},
	)
	return log, true, err
}

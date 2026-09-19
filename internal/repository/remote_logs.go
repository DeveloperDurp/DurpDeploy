package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
	"durpdeploy/internal/logscrub"
)

type RemoteLogEvent struct {
	Sequence int64  `json:"sequence"`
	Line     string `json:"line"`
}

func (r *Repository) AppendRemoteDeploymentLogs(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	events []RemoteLogEvent,
	scrubber *logscrub.Scrubber,
) ([]db.DeploymentLog, error) {
	inserted := make([]db.DeploymentLog, 0, len(events))
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		inserted, err = r.appendRemoteDeploymentLogs(
			ctx, q, identity, events, scrubber, false,
		)
		return err
	})
	if err != nil {
		return nil, err
	}
	return inserted, nil
}

func (r *Repository) appendRemoteDeploymentLogs(
	ctx context.Context,
	q *db.Queries,
	identity RemoteLifecycleClaim,
	events []RemoteLogEvent,
	scrubber *logscrub.Scrubber,
	flush bool,
) ([]db.DeploymentLog, error) {
	var claim db.RemoteDeploymentClaim
	var deployment db.Deployment
	var err error
	if flush {
		claim, deployment, err = lockRemoteLifecycleRows(
			ctx, q, identity, true,
		)
	} else {
		claim, deployment, err = lockRemoteLifecycle(ctx, q, identity, true)
	}
	if err != nil {
		return nil, err
	}
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return nil, err
	}
	if !flush && (deployment.Status != "running" ||
		(claim.State != "started" && claim.State != "cancel_requested")) {
		return nil, ErrRemoteLifecycleConflict
	}
	pending, err := r.decodeRemoteLogBuffer(claim.LogBufferCiphertext)
	if err != nil {
		return nil, err
	}
	lastSequence, err := q.GetLastRemoteDeploymentLogSequence(
		ctx, identity.DeploymentID,
	)
	if err != nil {
		return nil, err
	}
	ready, remaining, err := prepareRemoteLogEvents(
		pending, events, lastSequence, scrubber, flush,
	)
	if err != nil {
		return nil, err
	}
	inserted := make([]db.DeploymentLog, 0, len(ready))
	for _, event := range ready {
		if event.Sequence < 0 {
			return nil, ErrInvalidRemoteLog
		}
		log, created, err := appendRemoteDeploymentLog(
			ctx, q, identity.DeploymentID, now, event,
		)
		if err != nil {
			return nil, err
		}
		if created {
			inserted = append(inserted, log)
		}
	}
	buffer, err := r.encodeRemoteLogBuffer(remaining)
	if err != nil {
		return nil, err
	}
	changed, err := q.UpdateRemoteDeploymentLogBuffer(
		ctx,
		db.UpdateRemoteDeploymentLogBufferParams{
			LogBufferCiphertext: buffer,
			DeploymentID:        identity.DeploymentID,
			AgentID:             identity.AgentID,
			ClaimTokenHash:      identity.ClaimTokenHash,
		},
	)
	if err != nil || changed != 1 {
		return nil, transitionError(err)
	}
	return inserted, nil
}

func (r *Repository) FlushRemoteDeploymentLogs(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	scrubber *logscrub.Scrubber,
) ([]db.DeploymentLog, error) {
	var inserted []db.DeploymentLog
	err := r.WithTx(ctx, func(q *db.Queries) error {
		var err error
		inserted, err = r.appendRemoteDeploymentLogs(
			ctx, q, identity, nil, scrubber, true,
		)
		return err
	})
	return inserted, err
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

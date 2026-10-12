package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/logscrub"
)

func (r *Repository) AppendRemoteStepLogs(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	events []RemoteLogEvent,
	scrubber *logscrub.Scrubber,
) ([]db.DeploymentLog, bool, error) {
	inserted := make([]db.DeploymentLog, 0, len(events))
	handled := false
	err := r.withDeploymentLogTx(
		ctx,
		identity.DeploymentID,
		func(q *db.Queries) error {
			run, err := remoteStepRun(ctx, q, identity)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			handled = true
			inserted, err = r.appendRemoteStepLogs(
				ctx, q, identity, run, events, scrubber, false,
			)
			return err
		},
	)
	return inserted, handled, err
}

func (r *Repository) appendRemoteStepLogs(
	ctx context.Context,
	q *db.Queries,
	identity RemoteLifecycleClaim,
	run db.RemoteStepRun,
	events []RemoteLogEvent,
	scrubber *logscrub.Scrubber,
	flush bool,
) ([]db.DeploymentLog, error) {
	if !flush && run.State != "started" && run.State != "cancel_requested" {
		return nil, ErrRemoteLifecycleConflict
	}
	steps, err := q.ListDeploymentSteps(ctx, run.DeploymentID)
	if err != nil || run.StepIndex < 0 || run.StepIndex >= int64(len(steps)) {
		return nil, transitionError(err)
	}
	pending, err := r.decodeRemoteLogBuffer(run.LogBufferCiphertext)
	if err != nil {
		return nil, err
	}
	lastSequence, err := q.GetLastRemoteStepLogSequence(
		ctx,
		db.GetLastRemoteStepLogSequenceParams{
			DeploymentID: run.DeploymentID,
			StepIndex:    run.StepIndex,
			AgentID:      run.AgentID,
		},
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
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return nil, err
	}
	inserted := make([]db.DeploymentLog, 0, len(ready))
	for _, event := range ready {
		if event.Sequence < 0 {
			return nil, ErrInvalidRemoteLog
		}
		existing, err := q.GetRemoteStepLogBySequence(
			ctx,
			db.GetRemoteStepLogBySequenceParams{
				DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
				AgentID: run.AgentID, Sequence: event.Sequence,
			},
		)
		if err == nil {
			_ = existing
			continue
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		line := event.Line
		if strings.HasPrefix(line, "Execution location:") {
			line = fmt.Sprintf(
				"%s agent_id=%q",
				line,
				scrubber.Scrub(run.AgentID),
			)
		}
		log, err := q.CreateStepDeploymentLog(
			ctx,
			db.CreateStepDeploymentLogParams{
				DeploymentID: run.DeploymentID,
				StepName: sql.NullString{
					String: steps[run.StepIndex].Name + " @ " + run.AgentID,
					Valid:  true,
				},
				Line:      line,
				StepIndex: sql.NullInt64{Int64: run.StepIndex, Valid: true},
			},
		)
		if err != nil {
			return nil, err
		}
		if err := q.CreateRemoteStepLogSequence(
			ctx,
			db.CreateRemoteStepLogSequenceParams{
				DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
				AgentID: run.AgentID, Sequence: event.Sequence,
				LogID: log.ID,
			},
		); err != nil {
			return nil, err
		}
		log.CreatedAt = now
		inserted = append(inserted, log)
	}
	buffer, err := r.encodeRemoteLogBuffer(remaining)
	if err != nil {
		return nil, err
	}
	changed, err := q.UpdateRemoteStepLogBuffer(
		ctx,
		db.UpdateRemoteStepLogBufferParams{
			LogBufferCiphertext: buffer,
			DeploymentID:        run.DeploymentID, StepIndex: run.StepIndex,
			AgentID: run.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
		},
	)
	if err != nil || changed != 1 {
		return nil, transitionError(err)
	}
	return inserted, nil
}

func (r *Repository) FlushRemoteStepLogs(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	scrubber *logscrub.Scrubber,
) ([]db.DeploymentLog, bool, error) {
	var inserted []db.DeploymentLog
	handled := false
	err := r.withDeploymentLogTx(
		ctx,
		identity.DeploymentID,
		func(q *db.Queries) error {
			run, err := remoteStepRun(ctx, q, identity)
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			if err != nil {
				return err
			}
			handled = true
			inserted, err = r.appendRemoteStepLogs(
				ctx, q, identity, run, nil, scrubber, true,
			)
			return err
		},
	)
	return inserted, handled, err
}

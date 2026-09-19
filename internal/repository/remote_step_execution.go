package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/logscrub"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (r *Repository) QueueRemoteStepRuns(
	ctx context.Context,
	deploymentID int64,
	stepIndex int64,
) (int64, error) {
	created, err := r.Queries.CreateRemoteStepRuns(
		ctx,
		db.CreateRemoteStepRunsParams{
			DeploymentID: deploymentID,
			StepIndex:    stepIndex,
		},
	)
	if err == nil && created > 0 {
		r.notifyRemoteWork()
	}
	return created, err
}

func (r *Repository) ClaimRemoteStepPayload(
	ctx context.Context,
	agentID string,
	prepare func(RemotePayloadSnapshot) (RemotePreparedClaim, error),
) (RemoteClaim, bool, error) {
	type claimResult struct {
		claim   RemoteClaim
		claimed bool
	}
	result, err := withSQLiteBusyRetryValue(
		ctx,
		func() (claimResult, error) {
			attempt := claimResult{}
			err := r.WithTx(ctx, func(q *db.Queries) error {
				waiting, err := q.ListWaitingRemoteStepRuns(ctx, agentID)
				if err != nil || len(waiting) == 0 {
					return err
				}
				candidate := waiting[0]
				snapshot, err := r.remotePayloadSnapshot(
					ctx, q, agentID, candidate.DeploymentID,
				)
				if err != nil {
					return err
				}
				if candidate.StepIndex < 0 ||
					candidate.StepIndex >= int64(len(snapshot.Steps)) {
					return fmt.Errorf(
						"remote step index %d is out of range",
						candidate.StepIndex,
					)
				}
				snapshot.Steps = snapshot.Steps[candidate.StepIndex : candidate.StepIndex+1]
				prepared, err := prepare(snapshot)
				if err != nil {
					return err
				}
				now, err := q.CurrentUnixTime(ctx)
				if err != nil {
					return err
				}
				changed, err := q.ClaimRemoteStepRun(
					ctx,
					db.ClaimRemoteStepRunParams{
						ClaimTokenHash: prepared.TokenHash,
						Ciphertext: sql.NullString{
							String: string(prepared.Ciphertext), Valid: true,
						},
						ClaimExpiresAt: sql.NullInt64{
							Int64: now + int64(
								agentproto.PreStartClaimTimeout/time.Second,
							),
							Valid: true,
						},
						Now:          sql.NullInt64{Int64: now, Valid: true},
						DeploymentID: candidate.DeploymentID,
						StepIndex:    candidate.StepIndex,
						AgentID:      agentID,
					},
				)
				if err != nil || changed == 0 {
					return err
				}
				attempt.claimed = true
				attempt.claim = RemoteClaim{
					DeploymentID: candidate.DeploymentID,
					Token:        prepared.Token,
					Ciphertext:   prepared.Ciphertext,
				}
				return nil
			})
			if err != nil {
				return claimResult{}, err
			}
			return attempt, nil
		},
	)
	return result.claim, result.claimed, err
}

func (r *Repository) StartRemoteStep(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) (bool, error) {
	handled := false
	err := r.WithTx(ctx, func(q *db.Queries) error {
		run, err := remoteStepRun(ctx, q, identity)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		handled = true
		if run.State == "started" || run.State == "cancel_requested" {
			return nil
		}
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		changed, err := q.StartRemoteStepRun(ctx, db.StartRemoteStepRunParams{
			Now:          sql.NullInt64{Int64: now, Valid: true},
			DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
			AgentID: run.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
		})
		if err != nil || changed != 1 {
			return transitionError(err)
		}
		return nil
	})
	return handled, err
}

func (r *Repository) HeartbeatRemoteStep(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) (RemoteHeartbeatResult, bool, error) {
	result := RemoteHeartbeatResult{}
	handled := false
	err := r.WithTx(ctx, func(q *db.Queries) error {
		run, err := remoteStepRun(ctx, q, identity)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		handled = true
		if run.State != "started" && run.State != "cancel_requested" {
			return ErrRemoteLifecycleConflict
		}
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		changed, err := q.HeartbeatRemoteStepRun(
			ctx,
			db.HeartbeatRemoteStepRunParams{
				Now:          sql.NullInt64{Int64: now, Valid: true},
				DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
				AgentID: run.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
			},
		)
		if err != nil || changed != 1 {
			return transitionError(err)
		}
		result.CancelRequested = run.State == "cancel_requested"
		return nil
	})
	return result, handled, err
}

func (r *Repository) FinishRemoteStep(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	state string,
) (bool, error) {
	handled := false
	err := r.WithTx(ctx, func(q *db.Queries) error {
		run, err := remoteStepRun(ctx, q, identity)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		handled = true
		if run.State == state {
			return nil
		}
		if state != "succeeded" && state != "failed" {
			return ErrRemoteLifecycleConflict
		}
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		changed, err := q.FinishRemoteStepRun(ctx, db.FinishRemoteStepRunParams{
			State: state, Now: sql.NullInt64{Int64: now, Valid: true},
			DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
			AgentID: run.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
		})
		if err != nil || changed != 1 {
			return transitionError(err)
		}
		return nil
	})
	return handled, err
}

func remoteStepRun(
	ctx context.Context,
	q *db.Queries,
	identity RemoteLifecycleClaim,
) (db.RemoteStepRun, error) {
	locked, err := q.LockRemoteStepRun(ctx, db.LockRemoteStepRunParams{
		DeploymentID: identity.DeploymentID,
		AgentID:      identity.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
	})
	if err != nil {
		return db.RemoteStepRun{}, err
	}
	if locked != 1 {
		return db.RemoteStepRun{}, sql.ErrNoRows
	}
	return q.GetRemoteStepRunByClaim(ctx, db.GetRemoteStepRunByClaimParams{
		DeploymentID: identity.DeploymentID,
		AgentID:      identity.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
	})
}

func (r *Repository) AppendRemoteStepLogs(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	events []RemoteLogEvent,
	scrubber *logscrub.Scrubber,
) ([]db.DeploymentLog, bool, error) {
	inserted := make([]db.DeploymentLog, 0, len(events))
	handled := false
	err := r.WithTx(ctx, func(q *db.Queries) error {
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
	})
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
		log, err := q.CreateDeploymentLog(ctx, db.CreateDeploymentLogParams{
			DeploymentID: run.DeploymentID,
			StepName: sql.NullString{
				String: steps[run.StepIndex].Name + " @ " + run.AgentID,
				Valid:  true,
			},
			Line: event.Line,
		})
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
	err := r.WithTx(ctx, func(q *db.Queries) error {
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
	})
	return inserted, handled, err
}

func (r *Repository) AcknowledgeRemoteStepCancellation(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) (bool, bool, error) {
	changed := false
	handled := false
	err := r.WithTx(ctx, func(q *db.Queries) error {
		run, err := remoteStepRun(ctx, q, identity)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		handled = true
		if run.State == "cancelled" {
			return nil
		}
		now, err := q.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		rows, err := q.AcknowledgeRemoteStepCancellation(
			ctx,
			db.AcknowledgeRemoteStepCancellationParams{
				Now:          sql.NullInt64{Int64: now, Valid: true},
				DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
				AgentID: run.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
			},
		)
		if err != nil || rows != 1 {
			return transitionError(err)
		}
		changed = true
		return nil
	})
	return changed, handled, err
}

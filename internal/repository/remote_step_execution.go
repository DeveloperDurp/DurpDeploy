package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

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

func (r *Repository) FinishRemoteStep(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	state string,
) (bool, error) {
	handled := false
	err := r.WithDeploymentTx(
		ctx,
		identity.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
			var err error
			handled, err = finishRemoteStep(ctx, q, identity, state)
			return err
		},
	)
	return handled, err
}

func finishRemoteStep(
	ctx context.Context,
	q *db.Queries,
	identity RemoteLifecycleClaim,
	state string,
) (bool, error) {
	run, err := remoteStepRun(ctx, q, identity)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if run.State == state {
		return true, nil
	}
	if state == "succeeded" {
		stage, err := q.HasStagingInput(ctx, db.HasStagingInputParams{
			DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
		})
		if err != nil {
			return true, err
		}
		if stage != 0 {
			if _, err := q.GetStagingSnapshot(ctx, db.GetStagingSnapshotParams{
				DeploymentID: run.DeploymentID, StepIndex: run.StepIndex, Producer: run.AgentID,
			}); err != nil {
				return true, ErrRemoteLifecycleConflict
			}
		}
	}
	if state != "succeeded" && state != "failed" &&
		state != "cleanup_unconfirmed" {
		return true, ErrRemoteLifecycleConflict
	}
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return true, err
	}
	changed, err := q.FinishRemoteStepRun(ctx, db.FinishRemoteStepRunParams{
		State: state, Now: sql.NullInt64{Int64: now, Valid: true},
		DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
		AgentID: run.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
	})
	if err != nil || changed != 1 {
		return true, transitionError(err)
	}
	return true, nil
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

func (r *Repository) AcknowledgeRemoteStepCancellation(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) (bool, bool, error) {
	changed := false
	handled := false
	err := r.WithDeploymentTx(
		ctx,
		identity.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
			var err error
			changed, handled, err = acknowledgeRemoteStepCancellation(
				ctx,
				q,
				identity,
			)
			return err
		},
	)
	return changed, handled, err
}

func acknowledgeRemoteStepCancellation(
	ctx context.Context,
	q *db.Queries,
	identity RemoteLifecycleClaim,
) (bool, bool, error) {
	run, err := remoteStepRun(ctx, q, identity)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	if run.State == "cancelled" {
		return false, true, nil
	}
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return false, true, err
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
		return false, true, transitionError(err)
	}
	return true, true, nil
}

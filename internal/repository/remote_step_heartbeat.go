package repository

import (
	"context"
	"database/sql"
	"errors"

	"durpdeploy/internal/db"
)

func (r *Repository) HeartbeatRemoteStep(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) (RemoteHeartbeatResult, bool, error) {
	result := RemoteHeartbeatResult{}
	handled := false
	err := r.WithTx(ctx, func(queries *db.Queries) error {
		run, err := remoteStepRun(ctx, queries, identity)
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
		now, err := queries.CurrentUnixTime(ctx)
		if err != nil {
			return err
		}
		changed, err := queries.HeartbeatRemoteStepRun(
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
		changed, err = queries.TouchAgentHeartbeat(
			ctx,
			db.TouchAgentHeartbeatParams{
				Now: sql.NullInt64{
					Int64: now,
					Valid: true,
				},
				ID: identity.AgentID,
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

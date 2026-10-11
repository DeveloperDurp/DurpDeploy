package repository

import (
	"context"
	"database/sql"

	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (r *Repository) LiveStagingRun(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) (db.RemoteStepRun, error) {
	return liveStagingRun(ctx, r.Queries, identity)
}

func liveStagingRun(
	ctx context.Context,
	q *db.Queries,
	identity RemoteLifecycleClaim,
) (db.RemoteStepRun, error) {
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return db.RemoteStepRun{}, err
	}
	allowed, err := q.HasLiveArtifactClaim(ctx, db.HasLiveArtifactClaimParams{
		DeploymentID: identity.DeploymentID, AgentID: identity.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
		Now: sql.NullInt64{
			Int64: now,
			Valid: true,
		}, StaleBefore: sql.NullInt64{Int64: now - int64(agentproto.LostThreshold.Seconds()), Valid: true},
	})
	if err != nil {
		return db.RemoteStepRun{}, err
	}
	if allowed != 1 {
		return db.RemoteStepRun{}, ErrRemoteLifecycleConflict
	}
	run, err := q.GetRemoteStepRunByClaim(ctx, db.GetRemoteStepRunByClaimParams{
		DeploymentID: identity.DeploymentID, AgentID: identity.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
	})
	if err != nil {
		return db.RemoteStepRun{}, ErrRemoteLifecycleConflict
	}
	input, err := q.HasStagingInput(
		ctx,
		db.HasStagingInputParams{
			DeploymentID: run.DeploymentID,
			StepIndex:    run.StepIndex,
		},
	)
	if err != nil {
		return db.RemoteStepRun{}, err
	}
	if input != 1 {
		return db.RemoteStepRun{}, ErrRemoteLifecycleConflict
	}
	return run, nil
}

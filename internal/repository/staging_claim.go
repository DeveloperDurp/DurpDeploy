package repository

import (
	"context"
	"database/sql"
	"errors"

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
	if err := remoteInputClaimLive(ctx, q, identity); err != nil {
		return db.RemoteStepRun{}, err
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

// AuthorizeRemoteStepInput records that bytes can reach an unstarted agent.
// The run lock makes this decision exclusive with retiring its claim.
func (r *Repository) AuthorizeRemoteStepInput(
	ctx context.Context,
	identity RemoteLifecycleClaim,
) error {
	err := r.WithDeploymentTx(ctx, identity.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
			run, err := remoteStepRun(ctx, q, identity)
			if err != nil {
				return err
			}
			if err := remoteInputClaimLive(ctx, q, identity); err != nil {
				return err
			}
			if run.State != "claimed" {
				return nil
			}
			return q.MarkPrestartRemoteStepInput(
				ctx,
				db.MarkPrestartRemoteStepInputParams{
					DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
					AgentID: run.AgentID,
				},
			)
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRemoteLifecycleConflict
	}
	return err
}

func remoteInputClaimLive(
	ctx context.Context, q *db.Queries, identity RemoteLifecycleClaim,
) error {
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return err
	}
	allowed, err := q.HasLiveArtifactClaim(ctx, db.HasLiveArtifactClaimParams{
		DeploymentID: identity.DeploymentID, AgentID: identity.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
		Now: sql.NullInt64{
			Int64: now,
			Valid: true,
		}, StaleBefore: sql.NullInt64{Int64: now - int64(agentproto.LostThreshold.Seconds()), Valid: true},
	})
	if err != nil {
		return err
	}
	if allowed != 1 {
		return ErrRemoteLifecycleConflict
	}
	return nil
}

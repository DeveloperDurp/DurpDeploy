package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func (r *Repository) QueueRemoteStepRuns(
	ctx context.Context,
	deploymentID int64,
	stepIndex int64,
) (int64, error) {
	var created int64
	err := r.WithDeploymentTx(
		ctx,
		deploymentID,
		func(ctx context.Context, q *db.Queries) error {
			d, err := q.GetDeployment(ctx, deploymentID)
			if err != nil {
				return err
			}
			if err := advanceEnvironmentQueue(
				ctx,
				q,
				d.EnvironmentID,
			); err != nil {
				return err
			}
			created, err = q.CreateRemoteStepRuns(ctx,
				db.CreateRemoteStepRunsParams{
					DeploymentID: deploymentID, StepIndex: stepIndex,
				})
			if err != nil {
				return err
			}
			return r.validateQueuedRemotePolicies(
				ctx,
				q,
				deploymentID,
				stepIndex,
			)
		},
	)
	if err != nil {
		return 0, err
	}
	if created > 0 {
		r.notifyRemoteWork()
	}
	return created, err
}

// ClaimRemoteStepPayload may repeat prepare after a rolled-back claim.
// The callback must only prepare the payload, without external side effects.
func (r *Repository) ClaimRemoteStepPayload(
	ctx context.Context,
	agentID string,
	prepare func(RemotePayloadSnapshot) (RemotePreparedClaim, error),
) (RemoteClaim, bool, error) {
	type claimResult struct {
		claim   RemoteClaim
		claimed bool
	}
	result, err := withPollClaimRetryValue(
		ctx,
		func() (claimResult, error) {
			attempt := claimResult{}
			waiting, err := r.Queries.ListWaitingRemoteStepRuns(ctx, agentID)
			if err != nil || len(waiting) == 0 {
				return attempt, err
			}
			candidate := waiting[0]
			err = r.WithDeploymentTx(
				ctx,
				candidate.DeploymentID,
				func(ctx context.Context, q *db.Queries) error {
					claim, claimed, err := r.claimRemoteStepPayload(
						ctx, q, agentID, candidate, prepare,
					)
					if claimed {
						attempt.claimed = true
						attempt.claim = claim
					}
					return err
				},
			)
			if err != nil {
				return claimResult{}, err
			}
			return attempt, nil
		},
	)
	return result.claim, result.claimed, err
}

func (r *Repository) claimRemoteStepPayload(
	ctx context.Context,
	q *db.Queries,
	agentID string,
	candidate db.ListWaitingRemoteStepRunsRow,
	prepare func(RemotePayloadSnapshot) (RemotePreparedClaim, error),
) (RemoteClaim, bool, error) {
	locked, err := q.LockClaimAgent(ctx, agentID)
	if err != nil || locked == 0 {
		return RemoteClaim{}, false, err
	}
	agent, err := q.GetAgent(ctx, agentID)
	if err != nil || agent.Draining != 0 {
		return RemoteClaim{}, false, err
	}
	waiting, err := q.ListWaitingRemoteStepRuns(ctx, agentID)
	if err != nil || len(waiting) == 0 {
		return RemoteClaim{}, false, err
	}
	if waiting[0].DeploymentID != candidate.DeploymentID ||
		waiting[0].StepIndex != candidate.StepIndex {
		return RemoteClaim{}, false, nil
	}
	snapshot, err := r.remoteStepPayloadSnapshot(ctx, q, agentID, candidate)
	if errors.Is(err, agentexecution.ErrLimits) {
		return RemoteClaim{}, false, failInvalidRemoteSteps(
			ctx,
			q,
			candidate.DeploymentID,
			err,
		)
	}
	if err != nil {
		return RemoteClaim{}, false, err
	}
	prepared, err := prepare(snapshot)
	if errors.Is(err, containerenv.ErrEnvironment) {
		return RemoteClaim{}, false, failInvalidRemoteSteps(
			ctx,
			q,
			candidate.DeploymentID,
			err,
		)
	}
	if err != nil {
		return RemoteClaim{}, false, err
	}
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return RemoteClaim{}, false, err
	}
	changed, err := q.ClaimRemoteStepRun(
		ctx,
		db.ClaimRemoteStepRunParams{
			ClaimTokenHash: prepared.TokenHash,
			Ciphertext: sql.NullString{
				String: string(
					prepared.Ciphertext,
				), Valid: true,
			},
			ClaimExpiresAt: sql.NullInt64{
				Int64: now + int64(
					agentproto.PreStartClaimTimeout/time.Second,
				),
				Valid: true,
			},
			Now: sql.NullInt64{
				Int64: now,
				Valid: true,
			},
			DeploymentID: candidate.DeploymentID,
			StepIndex:    candidate.StepIndex,
			AgentID:      agentID,
		},
	)
	if err != nil || changed == 0 {
		return RemoteClaim{}, false, err
	}
	return RemoteClaim{
		DeploymentID: candidate.DeploymentID,
		Token:        prepared.Token,
		Ciphertext:   prepared.Ciphertext,
	}, true, nil
}

func (r *Repository) remoteStepPayloadSnapshot(
	ctx context.Context,
	q *db.Queries,
	agentID string,
	candidate db.ListWaitingRemoteStepRunsRow,
) (RemotePayloadSnapshot, error) {
	snapshot, err := r.remotePayloadSnapshot(
		ctx,
		q,
		agentID,
		candidate.DeploymentID,
	)
	if err != nil {
		return RemotePayloadSnapshot{}, err
	}
	if candidate.StepIndex < 0 ||
		candidate.StepIndex >= int64(len(snapshot.Steps)) {
		return RemotePayloadSnapshot{}, fmt.Errorf(
			"remote step index %d is out of range",
			candidate.StepIndex,
		)
	}
	snapshot.Steps = snapshot.Steps[candidate.StepIndex : candidate.StepIndex+1]
	stage, err := q.HasStagingInput(ctx, db.HasStagingInputParams(candidate))
	if err != nil {
		return RemotePayloadSnapshot{}, err
	}
	snapshot.Staging = stage != 0
	return snapshot, nil
}

func failInvalidRemoteSteps(ctx context.Context, q *db.Queries,
	deploymentID int64, validationErr error,
) error {
	if _, err := q.CreateDeploymentLog(ctx, db.CreateDeploymentLogParams{
		DeploymentID: deploymentID, Line: validationErr.Error(),
	}); err != nil {
		return err
	}
	now, err := q.CurrentUnixTime(ctx)
	if err != nil {
		return err
	}
	if err := q.FailInvalidWaitingRemoteSteps(ctx,
		db.FailInvalidWaitingRemoteStepsParams{
			DeploymentID: deploymentID,
			Now:          sql.NullInt64{Int64: now, Valid: true},
		}); err != nil {
		return err
	}
	return q.FailInvalidRemoteStepDeployment(ctx,
		db.FailInvalidRemoteStepDeploymentParams{
			DeploymentID: deploymentID,
			Now:          sql.NullInt64{Int64: now, Valid: true},
		})
}

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

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
			return err
		},
	)
	if err == nil && created > 0 {
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
					locked, err := q.LockClaimAgent(ctx, agentID)
					if err != nil || locked == 0 {
						return err
					}
					agent, err := q.GetAgent(ctx, agentID)
					if err != nil || agent.Draining != 0 {
						return err
					}
					waiting, err := q.ListWaitingRemoteStepRuns(ctx, agentID)
					if err != nil || len(waiting) == 0 {
						return err
					}
					if waiting[0].DeploymentID != candidate.DeploymentID ||
						waiting[0].StepIndex != candidate.StepIndex {
						return nil
					}
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
					stage, err := q.HasStagingInput(
						ctx,
						db.HasStagingInputParams(candidate),
					)
					if err != nil {
						return err
					}
					snapshot.Staging = stage != 0
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
						return err
					}
					attempt.claimed = true
					attempt.claim = RemoteClaim{
						DeploymentID: candidate.DeploymentID,
						Token:        prepared.Token,
						Ciphertext:   prepared.Ciphertext,
					}
					return nil
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

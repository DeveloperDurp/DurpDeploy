package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/logscrub"
)

func (r *Repository) StartRemoteStep(
	ctx context.Context,
	identity RemoteLifecycleClaim,
	scrubber *logscrub.Scrubber,
) ([]db.DeploymentLog, bool, error) {
	var inserted []db.DeploymentLog
	handled := false
	err := r.withDeploymentLogTx(ctx, identity.DeploymentID,
		func(q *db.Queries) error {
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
			changed, err := q.StartRemoteStepRun(
				ctx,
				db.StartRemoteStepRunParams{
					Now:          sql.NullInt64{Int64: now, Valid: true},
					DeploymentID: run.DeploymentID, StepIndex: run.StepIndex,
					AgentID: run.AgentID, ClaimTokenHash: identity.ClaimTokenHash,
				},
			)
			if err != nil || changed != 1 {
				return transitionError(err)
			}
			agent, err := q.GetAgent(ctx, run.AgentID)
			if err != nil {
				return err
			}
			steps, err := q.ListDeploymentSteps(ctx, run.DeploymentID)
			if err != nil || run.StepIndex < 0 ||
				run.StepIndex >= int64(len(steps)) {
				return transitionError(err)
			}
			log, err := q.CreateStepDeploymentLog(
				ctx,
				db.CreateStepDeploymentLogParams{
					DeploymentID: run.DeploymentID,
					StepName: sql.NullString{
						String: scrubber.Scrub(
							steps[run.StepIndex].Name + " @ " + run.AgentID,
						),
						Valid: true,
					},
					StepIndex: sql.NullInt64{Int64: run.StepIndex, Valid: true},
					Line: scrubber.Scrub(fmt.Sprintf(
						"Execution agent: agent=%q id=%q",
						scrubber.Scrub(agent.Name), run.AgentID,
					)),
				},
			)
			if err != nil {
				return err
			}
			inserted = append(inserted, log)
			return nil
		})
	return inserted, handled, err
}

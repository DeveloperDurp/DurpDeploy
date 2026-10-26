package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/db"
)

func (r *Repository) remoteResourcePolicy(
	ctx context.Context,
	q *db.Queries,
	agentID string,
) (string, error) {
	advertisement, err := q.GetAgentResourcePolicy(ctx, agentID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	if err := r.ResourceLimits().AgentPolicy().CheckCompatibility(advertisement); err != nil {
		return "", fmt.Errorf(
			"%w: agent %q resource policy is incompatible: %w",
			agentexecution.ErrLimits, agentID, err,
		)
	}
	return advertisement, nil
}

func (r *Repository) validateQueuedRemotePolicies(
	ctx context.Context,
	q *db.Queries,
	deploymentID, stepIndex int64,
) error {
	runs, err := q.ListRemoteStepRuns(ctx, db.ListRemoteStepRunsParams{
		DeploymentID: deploymentID, StepIndex: stepIndex,
	})
	if err != nil {
		return err
	}
	for _, run := range runs {
		if run.State == "waiting" {
			if _, err := r.remoteResourcePolicy(ctx, q, run.AgentID); err != nil {
				return err
			}
		}
	}
	return nil
}

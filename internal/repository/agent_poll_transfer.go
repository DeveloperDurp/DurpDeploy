package repository

import (
	"context"

	"durpdeploy/internal/db"
)

func (request AgentPollCapabilities) recordTransferCapabilities(
	ctx context.Context,
	q *db.Queries,
	agentID string,
) error {
	if request.Staging {
		// Request-local downgrades cannot invalidate queued staged work.
		if err := q.ClearAgentStagingCapability(ctx, agentID); err != nil {
			return err
		}
		if err := q.AddAgentStagingCapability(ctx, agentID); err != nil {
			return err
		}
	}
	if err := q.ClearAgentArtifactCapability(ctx, agentID); err != nil {
		return err
	}
	if request.Artifact {
		if err := q.AddAgentArtifactCapability(ctx, agentID); err != nil {
			return err
		}
	}
	if err := q.ClearAgentResourcePolicy(ctx, agentID); err != nil {
		return err
	}
	if request.ResourcePolicy != "" {
		return q.AddAgentResourcePolicy(ctx, db.AddAgentResourcePolicyParams{
			AgentID: agentID, Advertisement: request.ResourcePolicy,
		})
	}
	return nil
}

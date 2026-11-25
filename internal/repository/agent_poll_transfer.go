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
		return q.AddAgentArtifactCapability(ctx, agentID)
	}
	return nil
}

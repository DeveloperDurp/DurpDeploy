package repository

import (
	"context"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

var ErrAgentDeletionBlocked = errors.New(
	"agent has unresolved remote work; reconcile execution and cleanup before deleting",
)

// DeleteAgent retains a revoked tombstone for deployment history and pin reuse.
func (r *Repository) DeleteAgent(ctx context.Context, agentID string) error {
	err := r.WithQueueMaintenanceTx(
		ctx,
		func(ctx context.Context, q *db.Queries) error {
			if _, err := revokeAgent(ctx, q, agentID); err != nil {
				return err
			}
			changed, err := q.MarkAgentDeleted(ctx, agentID)
			if err != nil {
				return err
			}
			if changed != 1 {
				return ErrAgentDeletionBlocked
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	return nil
}

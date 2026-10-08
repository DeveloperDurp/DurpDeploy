package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

var ErrAgentDeletionBlocked = errors.New(
	"agent has unresolved remote work or buffered logs; reconcile execution, cleanup, and logs before deleting",
)

// DeleteAgent removes the registration and detaches retained deployment history.
func (r *Repository) DeleteAgent(ctx context.Context, agentID string) error {
	err := r.WithQueueMaintenanceTx(
		ctx,
		func(ctx context.Context, q *db.Queries) error {
			locked, err := q.LockAgentForDeletion(ctx, agentID)
			if err != nil {
				return err
			}
			if locked != 1 {
				return sql.ErrNoRows
			}
			blockers, err := q.CountAgentDeletionBlockers(ctx, agentID)
			if err != nil {
				return err
			}
			if blockers != 0 {
				return ErrAgentDeletionBlocked
			}
			if _, err := revokeAgent(ctx, q, agentID); err != nil {
				return err
			}
			reference := sql.NullString{String: agentID, Valid: true}
			active, err := q.CountAgentActiveDeploymentReferences(
				ctx,
				reference,
			)
			if err != nil {
				return err
			}
			if active != 0 {
				return ErrAgentDeletionBlocked
			}
			for _, remove := range []func(context.Context, string) error{
				q.PreserveAgentDeploymentCleanup,
				q.DeleteAgentRemoteLogSequences,
				q.DeleteAgentRemoteStepRuns,
				q.DeleteAgentRemoteClaims,
				q.DeleteAgentPairing,
				q.DeleteAgentLabels,
			} {
				if err := remove(ctx, agentID); err != nil {
					return err
				}
			}
			for _, detach := range []func(context.Context, sql.NullString) error{
				q.ClearAgentDispatchPayloads,
				q.DetachAgentDeploymentAttempts,
				q.DetachAgentDeployments,
			} {
				if err := detach(ctx, reference); err != nil {
					return err
				}
			}
			_, err = q.DeleteAgentRegistration(ctx, agentID)
			return err
		},
	)
	if err != nil {
		return fmt.Errorf("delete agent: %w", err)
	}
	return nil
}

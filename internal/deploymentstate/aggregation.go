package deploymentstate

import (
	"context"
	"database/sql"
	"fmt"

	"durpdeploy/internal/db"
)

// RecomputeParent idempotently rolls a child transition into its fan-out root.
func RecomputeParent(
	ctx context.Context,
	queries *db.Queries,
	childID int64,
) error {
	child, err := queries.GetDeployment(ctx, childID)
	if err != nil {
		return fmt.Errorf("get child deployment: %w", err)
	}
	if !child.ParentDeploymentID.Valid {
		return nil
	}
	children, err := queries.ListDeploymentChildren(
		ctx,
		child.ParentDeploymentID,
	)
	if err != nil {
		return fmt.Errorf("list deployment children: %w", err)
	}
	if len(children) == 0 {
		return nil
	}
	status, startedAt, finishedAt := aggregate(children)
	if err := queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID: child.ParentDeploymentID.Int64, Status: status,
			StartedAt: startedAt, FinishedAt: finishedAt,
		},
	); err != nil {
		return fmt.Errorf("update fan-out parent: %w", err)
	}
	return nil
}

func FailUnclaimedAgentDeployments(
	ctx context.Context,
	queries *db.Queries,
	agentID string,
	now int64,
	reason string,
) error {
	dispatches, err := queries.ListWaitingDeploymentDispatchesForAgent(
		ctx,
		sql.NullString{String: agentID, Valid: true},
	)
	if err != nil {
		return fmt.Errorf("list unclaimed agent dispatches: %w", err)
	}
	for _, dispatch := range dispatches {
		updated, err := queries.FailWaitingDeploymentDispatch(
			ctx,
			db.FailWaitingDeploymentDispatchParams{
				Reason:     sql.NullString{String: reason, Valid: true},
				FinishedAt: sql.NullInt64{Int64: now, Valid: true},
				UpdatedAt:  now, DeploymentID: dispatch.DeploymentID,
				AgentID: sql.NullString{String: agentID, Valid: true},
			},
		)
		if err != nil {
			return fmt.Errorf("fail unclaimed agent dispatch: %w", err)
		}
		if updated == 0 {
			continue
		}
		if err := queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID: dispatch.DeploymentID, Status: "failed",
				FinishedAt: sql.NullInt64{Int64: now, Valid: true},
			},
		); err != nil {
			return fmt.Errorf("fail unclaimed deployment: %w", err)
		}
		if err := RecomputeParent(ctx, queries, dispatch.DeploymentID); err != nil {
			return err
		}
	}
	return nil
}

func RecomputeAgentParents(
	ctx context.Context,
	queries *db.Queries,
	agentID string,
) error {
	dispatches, err := queries.ListDeploymentDispatchesForAgent(
		ctx,
		sql.NullString{String: agentID, Valid: true},
	)
	if err != nil {
		return fmt.Errorf("list agent dispatches: %w", err)
	}
	for _, dispatch := range dispatches {
		if err := RecomputeParent(ctx, queries, dispatch.DeploymentID); err != nil {
			return err
		}
	}
	return nil
}

func aggregate(
	children []db.Deployment,
) (string, sql.NullInt64, sql.NullInt64) {
	startedAt := sql.NullInt64{}
	finishedAt := sql.NullInt64{}
	settled := true
	failed := false
	cancelled := false
	for _, child := range children {
		if child.StartedAt.Valid &&
			(!startedAt.Valid || child.StartedAt.Int64 < startedAt.Int64) {
			startedAt = child.StartedAt
		}
		if child.FinishedAt.Valid &&
			(!finishedAt.Valid || child.FinishedAt.Int64 > finishedAt.Int64) {
			finishedAt = child.FinishedAt
		}
		switch child.Status {
		case "succeeded":
		case "failed":
			failed = true
		case "cancelled":
			cancelled = true
		default:
			settled = false
		}
	}
	if !settled {
		finishedAt = sql.NullInt64{}
		if startedAt.Valid {
			return "running", startedAt, finishedAt
		}
		return "pending", startedAt, finishedAt
	}
	if failed {
		return "failed", startedAt, finishedAt
	}
	if cancelled {
		return "cancelled", startedAt, finishedAt
	}
	return "succeeded", startedAt, finishedAt
}

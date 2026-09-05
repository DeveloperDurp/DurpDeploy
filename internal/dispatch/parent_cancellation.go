package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/deploymentstate"
)

func (s *CancellationService) Cancel(
	ctx context.Context,
	deploymentID int64,
) (string, error) {
	deployment, err := s.repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		return "", fmt.Errorf("get deployment: %w", err)
	}
	if deployment.ParentDeploymentID.Valid {
		return "", ErrCancellationChild
	}
	children, err := s.repo.Queries.ListDeploymentChildren(
		ctx,
		sql.NullInt64{Int64: deploymentID, Valid: true},
	)
	if err != nil {
		return "", fmt.Errorf("list deployment children: %w", err)
	}
	if len(children) > 0 {
		return s.cancelParent(ctx, deployment, children)
	}
	return s.cancelOne(ctx, deployment)
}

func (s *CancellationService) cancelOne(
	ctx context.Context,
	deployment db.Deployment,
) (string, error) {
	deploymentID := deployment.ID
	if deployment.Status == "pending_approval" {
		updated, err := s.repo.Queries.CancelPendingApprovalDeployment(
			ctx,
			db.CancelPendingApprovalDeploymentParams{
				ID:         deploymentID,
				FinishedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
			},
		)
		if err != nil {
			return "", fmt.Errorf("cancel pending approval: %w", err)
		}
		if updated != 1 {
			return "", ErrCancellationState
		}
		return "cancelled", nil
	}
	if deployment.Status == "pending" {
		if err := s.cancelQueued(ctx, deploymentID); err == nil {
			return "cancelled", nil
		} else if !errors.Is(err, ErrCancellationState) {
			return "", fmt.Errorf("cancel queued remote deployment: %w", err)
		}
		reloaded, err := s.repo.Queries.GetDeployment(ctx, deploymentID)
		if err != nil {
			return "", fmt.Errorf(
				"reload deployment after queued cancellation: %w",
				err,
			)
		}
		deployment = reloaded
	}
	if deployment.Status != "running" {
		return "", ErrCancellationState
	}
	return s.cancelRunning(ctx, deploymentID)
}

func (s *CancellationService) cancelParent(
	ctx context.Context,
	parent db.Deployment,
	children []db.Deployment,
) (string, error) {
	if parent.Status == "succeeded" || parent.Status == "failed" ||
		parent.Status == "cancelled" {
		return parent.Status, nil
	}
	for _, child := range children {
		if child.Status == "succeeded" || child.Status == "failed" ||
			child.Status == "cancelled" {
			continue
		}
		if _, err := s.cancelOne(ctx, child); err != nil &&
			!errors.Is(err, ErrCancellationState) {
			return "", fmt.Errorf("cancel child %d: %w", child.ID, err)
		}
	}
	if err := deploymentstate.RecomputeParent(
		ctx, s.repo.Queries, children[0].ID,
	); err != nil {
		return "", err
	}
	updated, err := s.repo.Queries.GetDeployment(ctx, parent.ID)
	if err != nil {
		return "", fmt.Errorf("get cancelled parent: %w", err)
	}
	return updated.Status, nil
}

package dispatch

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

const cancellationDeadline = 30 * time.Second

var (
	ErrCancellationOwner = errors.New(
		"cancellation acknowledgement owner mismatch",
	)
	ErrCancellationState = errors.New(
		"deployment cannot be cancelled in its current state",
	)
)

type CancellationService struct {
	repo   *repository.Repository
	runner *runner.DeploymentRunner
	now    func() time.Time
}

func NewCancellationService(
	repo *repository.Repository,
	deploymentRunner *runner.DeploymentRunner,
) *CancellationService {
	return &CancellationService{
		repo: repo, runner: deploymentRunner, now: time.Now,
	}
}

func (s *CancellationService) Cancel(
	ctx context.Context,
	deploymentID int64,
) (string, error) {
	deployment, err := s.repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		return "", fmt.Errorf("get deployment: %w", err)
	}
	if deployment.Status == "pending_approval" {
		if err := s.repo.Queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:     deploymentID,
				Status: "cancelled",
				FinishedAt: sql.NullInt64{
					Int64: s.now().Unix(), Valid: true,
				},
			},
		); err != nil {
			return "", fmt.Errorf("cancel pending approval: %w", err)
		}
		return "cancelled", nil
	}
	if deployment.Status != "running" {
		return "", ErrCancellationState
	}

	dispatch, err := s.repo.Queries.GetDeploymentDispatch(ctx, deploymentID)
	if errors.Is(err, sql.ErrNoRows) || dispatch.Mode == "local" {
		return s.cancelLocal(deploymentID)
	}
	if err != nil {
		return "", fmt.Errorf("get deployment dispatch: %w", err)
	}
	if dispatch.Mode != "remote" ||
		(dispatch.State != "claimed" && dispatch.State != "started") {
		return "", ErrCancellationState
	}

	if err := s.repo.WithTx(ctx, func(q *db.Queries) error {
		_, err := q.RequestDeploymentDispatchCancellation(
			ctx,
			db.RequestDeploymentDispatchCancellationParams{
				CancelRequestedAt: sql.NullInt64{
					Int64: s.now().Unix(), Valid: true,
				},
				DeploymentID: deploymentID,
				CurrentState: dispatch.State,
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrCancellationState
		}
		return err
	}); err != nil {
		return "", fmt.Errorf("request remote cancellation: %w", err)
	}
	return "cancel_requested", nil
}

func (s *CancellationService) Acknowledge(
	ctx context.Context,
	deploymentID int64,
	agentID string,
	claimTokenHash []byte,
) error {
	return s.repo.WithTx(ctx, func(q *db.Queries) error {
		dispatch, err := q.GetDeploymentDispatch(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("get deployment dispatch: %w", err)
		}
		if !dispatch.AgentID.Valid || dispatch.AgentID.String != agentID ||
			!bytes.Equal(dispatch.ClaimTokenHash, claimTokenHash) {
			return ErrCancellationOwner
		}
		if dispatch.State == "cancelled" {
			return nil
		}
		if dispatch.State != "cancel_requested" {
			return ErrCancellationState
		}
		if _, err := q.AcknowledgeDeploymentDispatchCancellation(
			ctx,
			db.AcknowledgeDeploymentDispatchCancellationParams{
				FinishedAt: sql.NullInt64{
					Int64: s.now().Unix(),
					Valid: true,
				},
				DeploymentID:   deploymentID,
				AgentID:        sql.NullString{String: agentID, Valid: true},
				ClaimTokenHash: claimTokenHash,
			},
		); errors.Is(err, sql.ErrNoRows) {
			return ErrCancellationState
		} else if err != nil {
			return fmt.Errorf("acknowledge remote cancellation: %w", err)
		}
		if err := q.UpdateDeploymentStatus(ctx, db.UpdateDeploymentStatusParams{
			ID:         deploymentID,
			Status:     "cancelled",
			FinishedAt: sql.NullInt64{Int64: s.now().Unix(), Valid: true},
		}); err != nil {
			return fmt.Errorf("mark deployment cancelled: %w", err)
		}
		return nil
	})
}

func (s *CancellationService) Expire(
	ctx context.Context,
	now time.Time,
) (int64, error) {
	updated, err := s.repo.Queries.ExpireDeploymentDispatchCancellation(
		ctx,
		sql.NullInt64{
			Int64: now.Add(-cancellationDeadline).Unix(),
			Valid: true,
		},
	)
	if err != nil {
		return 0, fmt.Errorf("expire remote cancellation requests: %w", err)
	}
	return updated, nil
}

func (s *CancellationService) RecordLateResult(
	ctx context.Context,
	deploymentID int64,
	agentID string,
	claimTokenHash []byte,
) (bool, error) {
	recorded := false
	err := s.repo.WithTx(ctx, func(q *db.Queries) error {
		dispatch, err := q.GetDeploymentDispatch(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("get deployment dispatch: %w", err)
		}
		if !dispatch.AgentID.Valid || dispatch.AgentID.String != agentID ||
			!bytes.Equal(dispatch.ClaimTokenHash, claimTokenHash) {
			return ErrCancellationOwner
		}
		if dispatch.State == "claimed" || dispatch.State == "started" {
			return nil
		}
		if _, err := q.CreateAgentEventForDispatch(
			ctx,
			db.CreateAgentEventForDispatchParams{
				AgentID:      sql.NullString{String: agentID, Valid: true},
				DeploymentID: sql.NullInt64{Int64: deploymentID, Valid: true},
				EventType:    "late_result",
				CurrentState: sql.NullString{
					String: dispatch.State,
					Valid:  true,
				},
				ClaimTokenHash: claimTokenHash,
			},
		); err != nil {
			return fmt.Errorf("record late result event: %w", err)
		}
		recorded = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return recorded, nil
}

func (s *CancellationService) cancelLocal(deploymentID int64) (string, error) {
	if s.runner == nil {
		return "", ErrCancellationState
	}
	if err := s.runner.Cancel(deploymentID); err != nil {
		return "", fmt.Errorf("cancel local deployment: %w", err)
	}
	return "cancelled", nil
}

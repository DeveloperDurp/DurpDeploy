package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
)

var ErrScheduledOccurrenceClaimed = errors.New("scheduled occurrence already claimed")

type ScheduledRequest struct {
	CreateRequest
	ScheduleID int64
	DueAt      int64
	NextRunAt  int64
}

func (s *CreationService) CreateScheduled(
	ctx context.Context,
	request ScheduledRequest,
) (db.Deployment, error) {
	project, release, err := s.loadInputs(ctx, request.CreateRequest)
	if err != nil {
		return db.Deployment{}, err
	}
	state, err := gate.Evaluate(
		ctx, s.repo, project, release, request.EnvironmentID,
	)
	if err != nil {
		return db.Deployment{}, fmt.Errorf("check deployment gate: %w", err)
	}
	if !state.Deployable {
		return db.Deployment{}, fmt.Errorf(
			"%w: %s", ErrPromotionBlocked, state.Reason,
		)
	}
	requiresApproval, err := gate.RequiresApproval(
		ctx, s.repo, project, request.EnvironmentID,
	)
	if err != nil {
		return db.Deployment{}, fmt.Errorf("check approval: %w", err)
	}
	policy, err := s.schedulePolicy(ctx, request)
	if err != nil {
		return db.Deployment{}, err
	}

	var deployment db.Deployment
	err = s.repo.WithRoutingTx(ctx, func(q *db.Queries) error {
		advanced, err := q.AdvanceScheduledDeploymentOccurrence(
			ctx, db.AdvanceScheduledDeploymentOccurrenceParams{
				NextRunAt: request.NextRunAt,
				ExpectedNextRunAt: sql.NullInt64{
					Int64: request.DueAt, Valid: true,
				},
				ID: request.ScheduleID,
			},
		)
		if err != nil {
			return fmt.Errorf("advance scheduled occurrence: %w", err)
		}
		if advanced != 1 {
			return ErrScheduledOccurrenceClaimed
		}
		status := "pending"
		if requiresApproval {
			status = "pending_approval"
		}
		deployment, err = q.CreateDeployment(ctx, db.CreateDeploymentParams{
			ReleaseID: request.ReleaseID, EnvironmentID: request.EnvironmentID,
			Status: status, Note: sql.NullString{
				String: request.Note, Valid: request.Note != "",
			},
		})
		if err != nil {
			return fmt.Errorf("create scheduled deployment: %w", err)
		}
		if err := freezeIntentTx(ctx, q, deployment.ID, policy); err != nil {
			return err
		}
		_, err = q.ClaimScheduledDeploymentOccurrence(
			ctx, db.ClaimScheduledDeploymentOccurrenceParams{
				ScheduledDeploymentID: request.ScheduleID,
				DueAt:                 request.DueAt, DeploymentID: deployment.ID,
			},
		)
		return err
	})
	return deployment, err
}

func (s *CreationService) schedulePolicy(
	ctx context.Context,
	request ScheduledRequest,
) (Policy, error) {
	stored, err := s.repo.Queries.GetScheduledDeploymentRoutingPolicy(
		ctx, request.ScheduleID,
	)
	input := Input{Source: SourceSchedule, Mode: "inherit"}
	if err == nil {
		input.Mode = stored.TargetMode
		input.LabelID = stored.AgentLabelID.Int64
		input.Strategy = stored.AgentStrategy.String
	} else if !errors.Is(err, sql.ErrNoRows) {
		return Policy{}, fmt.Errorf("get schedule routing policy: %w", err)
	}
	return NewResolver(s.repo).Resolve(
		ctx, request.ProjectID, request.EnvironmentID, input,
	)
}

func (s *CreationService) DispatchFrozen(
	ctx context.Context,
	deploymentID int64,
) error {
	var local db.Deployment
	err := s.repo.WithRoutingTx(ctx, func(q *db.Queries) error {
		deployment, err := q.GetDeployment(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("get deployment: %w", err)
		}
		policy, err := policyForApproval(ctx, q, deployment)
		if err != nil {
			return err
		}
		runLocal, err := s.dispatcher.prepareTx(ctx, q, deployment, policy)
		if runLocal {
			local = deployment
		}
		return err
	})
	if err == nil && local.ID != 0 {
		s.dispatcher.runLocal(local)
	}
	return err
}

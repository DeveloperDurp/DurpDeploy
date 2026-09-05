package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
	"durpdeploy/internal/repository"
)

var (
	ErrProjectNotFound        = errors.New("project not found")
	ErrReleaseNotFound        = errors.New("release not found")
	ErrEnvironmentNotFound    = errors.New("environment not found")
	ErrReleaseProjectMismatch = errors.New("release does not belong to project")
	ErrPromotionBlocked       = errors.New(
		"deployment blocked by promotion gate",
	)
	ErrForceForbidden       = errors.New("only admins can force deployments")
	ErrDeploymentNotFound   = errors.New("deployment not found")
	ErrDeploymentNotPending = errors.New("deployment not pending approval")
)

type CreateRequest struct {
	ProjectID     int64
	ReleaseID     int64
	EnvironmentID int64
	Force         bool
	Admin         bool
	Note          string
	Routing       Input
}

type Approval struct {
	ApprovedBy     string
	ApproverUserID int64
}

type CreationService struct {
	repo       *repository.Repository
	dispatcher *Dispatcher
}

func NewCreationService(
	repo *repository.Repository,
	dispatcher *Dispatcher,
) *CreationService {
	return &CreationService{repo: repo, dispatcher: dispatcher}
}

func (s *CreationService) Create(
	ctx context.Context,
	request CreateRequest,
) (db.Deployment, error) {
	project, release, err := s.loadInputs(ctx, request)
	if err != nil {
		return db.Deployment{}, err
	}
	state, err := gate.Evaluate(
		ctx, s.repo, project, release, request.EnvironmentID,
	)
	if err != nil {
		return db.Deployment{}, fmt.Errorf("check deployment gate: %w", err)
	}
	blocked := !state.Deployable
	if blocked && (!request.Force || !state.Bypassable) {
		return db.Deployment{}, fmt.Errorf(
			"%w: %s", ErrPromotionBlocked, state.Reason,
		)
	}
	if blocked && !request.Admin {
		return db.Deployment{}, ErrForceForbidden
	}
	requiresApproval, err := gate.RequiresApproval(
		ctx, s.repo, project, request.EnvironmentID,
	)
	if err != nil {
		return db.Deployment{}, fmt.Errorf(
			"check deployment approval requirement: %w", err,
		)
	}
	policy, err := NewResolver(s.repo).Resolve(
		ctx, request.ProjectID, request.EnvironmentID, request.Routing,
	)
	if err != nil {
		return db.Deployment{}, err
	}

	var deployment db.Deployment
	var runLocal bool
	err = s.repo.WithRoutingTx(ctx, func(q *db.Queries) error {
		status := "pending"
		if requiresApproval {
			status = "pending_approval"
		}
		forced := int64(0)
		if blocked && request.Force {
			forced = 1
		}
		var createErr error
		deployment, createErr = q.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID: request.ReleaseID, EnvironmentID: request.EnvironmentID,
				Status: status, Forced: forced,
				Note: sql.NullString{
					String: request.Note,
					Valid:  request.Note != "",
				},
			},
		)
		if createErr != nil {
			return fmt.Errorf("create deployment: %w", createErr)
		}
		if requiresApproval {
			return freezeIntentTx(ctx, q, deployment.ID, policy)
		}
		runLocal, createErr = s.dispatcher.prepareTx(
			ctx, q, deployment, policy,
		)
		return createErr
	})
	if err != nil {
		return db.Deployment{}, err
	}
	if runLocal {
		s.dispatcher.runLocal(deployment)
	}
	return deployment, nil
}

func (s *CreationService) Approve(
	ctx context.Context,
	deploymentID int64,
	approval Approval,
) error {
	var local db.Deployment
	err := s.repo.WithRoutingTx(ctx, func(q *db.Queries) error {
		deployment, err := q.GetDeployment(ctx, deploymentID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrDeploymentNotFound
		}
		if err != nil {
			return fmt.Errorf("get deployment: %w", err)
		}
		updated, err := q.ApprovePendingDeployment(ctx, deploymentID)
		if err != nil {
			return fmt.Errorf("approve deployment: %w", err)
		}
		if updated != 1 {
			return ErrDeploymentNotPending
		}
		deployment.Status = "pending"
		policy, err := policyForApproval(ctx, q, deployment)
		if err != nil {
			return err
		}
		runLocal, err := s.dispatcher.prepareTx(ctx, q, deployment, policy)
		if err != nil {
			return err
		}
		if runLocal {
			local = deployment
		}
		_, err = q.CreateApproval(ctx, db.CreateApprovalParams{
			DeploymentID: deploymentID, ApprovedBy: approval.ApprovedBy,
			ApproverUserID: sql.NullInt64{
				Int64: approval.ApproverUserID, Valid: approval.ApproverUserID > 0,
			},
			RequiredApproverRole: "admin",
		})
		if err != nil {
			return fmt.Errorf("record approval: %w", err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	if local.ID != 0 {
		s.dispatcher.runLocal(local)
	}
	return nil
}

func (s *CreationService) loadInputs(
	ctx context.Context,
	request CreateRequest,
) (db.Project, db.Release, error) {
	project, err := s.repo.Queries.GetProject(ctx, request.ProjectID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, db.Release{}, ErrProjectNotFound
	}
	if err != nil {
		return db.Project{}, db.Release{}, fmt.Errorf("get project: %w", err)
	}
	release, err := s.repo.Queries.GetRelease(ctx, request.ReleaseID)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, db.Release{}, ErrReleaseNotFound
	}
	if err != nil {
		return db.Project{}, db.Release{}, fmt.Errorf("get release: %w", err)
	}
	if release.ProjectID != project.ID {
		return db.Project{}, db.Release{}, ErrReleaseProjectMismatch
	}
	if _, err := s.repo.Queries.GetEnvironment(
		ctx, request.EnvironmentID,
	); errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, db.Release{}, ErrEnvironmentNotFound
	} else if err != nil {
		return db.Project{}, db.Release{}, fmt.Errorf("get environment: %w", err)
	}
	return project, release, nil
}

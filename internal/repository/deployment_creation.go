package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
)

var (
	ErrDeploymentApprovalConflict = errors.New(
		"deployment is not pending approval",
	)
)

type ExecutionMode string

const (
	ExecutionLocal  ExecutionMode = "local"
	ExecutionRemote ExecutionMode = "remote"
)

type DeploymentResult struct {
	Deployment db.Deployment
	Mode       ExecutionMode
}

func (r *Repository) CreateDeployment(
	ctx context.Context,
	arg db.CreateDeploymentParams,
) (DeploymentResult, error) {
	var result DeploymentResult
	err := withSQLiteBusyRetry(ctx, func() error {
		result = DeploymentResult{}
		return r.WithTx(ctx, func(q *db.Queries) error {
			var createErr error
			result, createErr = r.createDeployment(ctx, q, arg)
			return createErr
		})
	})
	if err != nil {
		return DeploymentResult{}, fmt.Errorf("create deployment: %w", err)
	}
	return result, nil
}

func (r *Repository) createDeployment(
	ctx context.Context,
	q *db.Queries,
	arg db.CreateDeploymentParams,
) (DeploymentResult, error) {
	release, err := q.GetRelease(ctx, arg.ReleaseID)
	if err != nil {
		return DeploymentResult{}, fmt.Errorf("get release: %w", err)
	}
	steps, err := deploymentStepsFromRelease(release.StepsJson)
	if err != nil {
		return DeploymentResult{}, err
	}
	arg.AssignedAgentID = sql.NullString{}
	deployment, err := q.CreateDeployment(ctx, arg)
	if err != nil {
		return DeploymentResult{}, fmt.Errorf("insert deployment: %w", err)
	}
	if err := snapshotDeploymentSteps(
		ctx,
		q,
		deployment.ID,
		steps,
	); err != nil {
		return DeploymentResult{}, fmt.Errorf("snapshot release steps: %w", err)
	}
	return DeploymentResult{Deployment: deployment, Mode: ExecutionLocal}, nil
}

func (r *Repository) ApproveDeployment(
	ctx context.Context,
	approval db.CreateApprovalParams,
) (DeploymentResult, error) {
	var result DeploymentResult
	err := r.WithTx(ctx, func(q *db.Queries) error {
		locked, err := q.LockDeploymentApproval(ctx, approval.DeploymentID)
		if err != nil {
			return fmt.Errorf("lock deployment approval: %w", err)
		}
		if locked == 0 {
			if _, err := q.GetDeployment(
				ctx,
				approval.DeploymentID,
			); err != nil {
				return err
			}
			return ErrDeploymentApprovalConflict
		}
		deployment, err := q.GetDeployment(ctx, approval.DeploymentID)
		if err != nil {
			return fmt.Errorf("get deployment: %w", err)
		}
		if _, err := q.CreateApproval(ctx, approval); err != nil {
			return fmt.Errorf("create approval: %w", err)
		}
		changed, err := q.ApproveDeploymentStatus(ctx, approval.DeploymentID)
		if err != nil {
			return fmt.Errorf("approve deployment status: %w", err)
		}
		if changed != 1 {
			return ErrDeploymentApprovalConflict
		}
		deployment.Status = "pending"
		result = deploymentResult(deployment)
		return nil
	})
	if err != nil {
		return DeploymentResult{}, fmt.Errorf("approve deployment: %w", err)
	}
	if result.Mode == ExecutionRemote {
		r.notifyRemoteWork()
	}
	return result, nil
}

func deploymentResult(deployment db.Deployment) DeploymentResult {
	mode := ExecutionLocal
	if deployment.AssignedAgentID.Valid {
		mode = ExecutionRemote
	}
	return DeploymentResult{Deployment: deployment, Mode: mode}
}

func deploymentStepsFromRelease(raw string) ([]DeploymentStepSnapshot, error) {
	var source []struct {
		Name            string   `json:"name"`
		ScriptBody      string   `json:"script_body"`
		TimeoutSeconds  int64    `json:"timeout_seconds"`
		MaxRetries      int64    `json:"max_retries"`
		ExecutionTarget string   `json:"execution_target"`
		AgentSelectors  []string `json:"agent_selectors"`
	}
	if err := json.Unmarshal([]byte(raw), &source); err != nil {
		return nil, fmt.Errorf("decode release steps: %w", err)
	}
	steps := make([]DeploymentStepSnapshot, len(source))
	for i, step := range source {
		target := step.ExecutionTarget
		if target == "" {
			target = "local"
		}
		steps[i] = DeploymentStepSnapshot{
			CreateDeploymentStepParams: db.CreateDeploymentStepParams{
				Name:            step.Name,
				ScriptBody:      step.ScriptBody,
				TimeoutSeconds:  step.TimeoutSeconds,
				MaxRetries:      step.MaxRetries,
				ExecutionTarget: target,
			},
			Selectors: step.AgentSelectors,
		}
	}
	return steps, nil
}

package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

var (
	ErrDeploymentApprovalConflict = errors.New(
		"deployment is not pending approval",
	)
	ErrContainerCleanupUnconfirmed = errors.New(
		"container cleanup unconfirmed; retry after successful runtime reconciliation",
	)
	// ErrLegacyServerStep is returned when a release still contains a
	// local step without a container image. Local steps always run in a
	// container, so such releases cannot execute; recreate the step and
	// create a new release.
	ErrLegacyServerStep = errors.New(
		"legacy local step has no container image; " +
			"recreate the step and create a new release",
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
	if err := r.MaintainArtifactGates(ctx); err != nil {
		return DeploymentResult{}, err
	}
	var result DeploymentResult
	err := withSQLiteBusyRetry(ctx, func() error {
		result = DeploymentResult{}
		return r.withQueueTx(
			ctx,
			func(ctx context.Context, q *db.Queries) error {
				var createErr error
				result, createErr = r.createDeployment(ctx, q, arg)
				return createErr
			},
		)
	})
	if err != nil {
		return DeploymentResult{}, fmt.Errorf("create deployment: %w", err)
	}
	return result, nil
}

func (r *Repository) CreateDeploymentFromDeployment(
	ctx context.Context,
	arg db.CreateDeploymentParams,
	sourceDeploymentID int64,
) (DeploymentResult, error) {
	if err := r.MaintainArtifactGates(ctx); err != nil {
		return DeploymentResult{}, err
	}
	var result DeploymentResult
	err := withSQLiteBusyRetry(ctx, func() error {
		result = DeploymentResult{}
		return r.withQueueTx(
			ctx,
			func(ctx context.Context, q *db.Queries) error {
				var err error
				result, err = r.createDeploymentFromDeployment(ctx, q, arg,
					sourceDeploymentID)
				return err
			},
		)
	})
	if err != nil {
		return DeploymentResult{}, fmt.Errorf(
			"create deployment from deployment: %w", err)
	}
	return result, nil
}

func (r *Repository) ApproveDeployment(
	ctx context.Context,
	approval db.CreateApprovalParams,
) (DeploymentResult, error) {
	var result DeploymentResult
	err := r.WithDeploymentTx(
		ctx,
		approval.DeploymentID,
		func(ctx context.Context, q *db.Queries) error {
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
			changed, err := q.ApproveDeploymentStatus(
				ctx,
				approval.DeploymentID,
			)
			if err != nil {
				return fmt.Errorf("approve deployment status: %w", err)
			}
			if changed != 1 {
				return ErrDeploymentApprovalConflict
			}
			if err := advanceEnvironmentQueue(
				ctx,
				q,
				deployment.EnvironmentID,
			); err != nil {
				return err
			}
			deployment, err = q.GetDeployment(ctx, deployment.ID)
			if err != nil {
				return err
			}
			result = deploymentResult(deployment)
			return nil
		},
	)
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
		Name                 string   `json:"name"`
		ScriptBody           string   `json:"script_body"`
		Interpreter          string   `json:"interpreter"`
		TimeoutSeconds       int64    `json:"timeout_seconds"`
		MaxRetries           int64    `json:"max_retries"`
		ExecutionTarget      string   `json:"execution_target"`
		AgentExecutionMode   string   `json:"agent_execution_mode"`
		AgentSelectors       []string `json:"agent_selectors"`
		ContainerImage       string   `json:"container_image"`
		NetworkMode          string   `json:"network_mode"`
		ApprovalArtifactPath string   `json:"approval_artifact_path"`
		ApprovalReviewPath   string   `json:"approval_review_path"`
		ApprovalReviewFormat string   `json:"approval_review_format"`
		VariableNames        []string `json:"variable_names"`
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
		if target != "local" && target != "agent" {
			return nil, fmt.Errorf(
				"step %q has unknown execution target %q",
				step.Name, target,
			)
		}
		if target == "local" &&
			strings.TrimSpace(step.ContainerImage) == "" {
			return nil, fmt.Errorf(
				"step %q: %w", step.Name, ErrLegacyServerStep,
			)
		}
		mode, err := agentexecution.Parse(agentexecution.Config{
			Target: target, Mode: step.AgentExecutionMode,
			Image: step.ContainerImage, VariableNames: step.VariableNames,
		})
		if err != nil {
			return nil, fmt.Errorf("step %q: %w", step.Name, err)
		}
		if err := artifact.ValidateGateConfig(
			target,
			step.NetworkMode,
			step.ApprovalArtifactPath,
			step.ApprovalReviewPath,
			step.ApprovalReviewFormat,
		); err != nil {
			return nil, err
		}
		steps[i] = DeploymentStepSnapshot{
			CreateDeploymentStepParams: db.CreateDeploymentStepParams{
				Name:                 step.Name,
				ScriptBody:           step.ScriptBody,
				TimeoutSeconds:       step.TimeoutSeconds,
				MaxRetries:           step.MaxRetries,
				ExecutionTarget:      target,
				AgentExecutionMode:   string(mode),
				Interpreter:          step.Interpreter,
				ContainerImage:       step.ContainerImage,
				NetworkMode:          step.NetworkMode,
				ApprovalArtifactPath: step.ApprovalArtifactPath,
				ApprovalReviewPath:   step.ApprovalReviewPath,
				ApprovalReviewFormat: step.ApprovalReviewFormat,
				VariableNames:        marshalVariableNames(step.VariableNames),
			},
			Selectors: step.AgentSelectors,
		}
	}
	return steps, nil
}

// marshalVariableNames encodes the optional restriction as the JSON array text
// the deployment_steps.variable_names column stores. Missing (legacy) and
// unrestricted values both store "[]", never "".
func marshalVariableNames(variableNames []string) string {
	if variableNames == nil {
		variableNames = []string{}
	}
	encoded, err := json.Marshal(variableNames)
	if err != nil {
		return "[]"
	}
	return string(encoded)
}

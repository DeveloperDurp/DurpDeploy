package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/db"
	"durpdeploy/internal/verification"
)

func snapshotVerification(
	ctx context.Context, q *db.Queries, deployment db.Deployment,
	steps []DeploymentStepSnapshot,
) ([]DeploymentStepSnapshot, error) {
	if deployment.Kind != "deployment" {
		return steps, nil
	}
	environment, err := q.GetEnvironment(ctx, deployment.EnvironmentID)
	if err != nil {
		return nil, fmt.Errorf("get verification environment: %w", err)
	}
	if environment.VerificationType == "" {
		return steps, nil
	}
	index := int64(len(steps))
	if environment.VerificationType == string(verification.Bash) {
		if len(steps) == 0 {
			return nil, fmt.Errorf(
				"Bash verification needs a deployment step: %w",
				verification.ErrInvalid,
			)
		}
		step := steps[len(steps)-1]
		step.Name = "Post-deployment verification"
		// The encrypted target is the source of truth, not a plaintext script copy.
		step.ScriptBody = ""
		step.Interpreter = "bash"
		step.TimeoutSeconds = environment.VerificationTimeoutSeconds
		step.MaxRetries = 0
		step.SourceStepID.Valid = false
		steps = append(steps, step)
	}
	err = q.CreateDeploymentVerification(ctx,
		db.CreateDeploymentVerificationParams{
			DeploymentID:   deployment.ID,
			Type:           environment.VerificationType,
			Target:         environment.VerificationTarget,
			TimeoutSeconds: environment.VerificationTimeoutSeconds,
			StepIndex:      index,
		})
	return steps, err
}

func (r *Repository) VerificationStepScript(
	ctx context.Context, q *db.Queries, step db.DeploymentStep,
) (string, error) {
	check, err := q.GetDeploymentVerification(ctx, step.DeploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		return step.ScriptBody, nil
	}
	if err != nil {
		return "", err
	}
	if check.Type != string(verification.Bash) ||
		check.StepIndex != step.StepIndex {
		return step.ScriptBody, nil
	}
	return r.DecryptVerificationTarget(check.Target)
}

package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
)

func (r *Repository) createDeploymentFromDeployment(
	ctx context.Context, q *db.Queries, arg db.CreateDeploymentParams,
	sourceDeploymentID int64,
) (DeploymentResult, error) {
	if _, err := q.LockDeploymentEnvironment(
		ctx,
		arg.EnvironmentID,
	); err != nil {
		return DeploymentResult{}, err
	}
	if err := lockRelease(ctx, q, arg.ReleaseID); err != nil {
		return DeploymentResult{}, err
	}
	source, err := q.GetDeployment(ctx, sourceDeploymentID)
	if err != nil {
		return DeploymentResult{}, fmt.Errorf("get source deployment: %w", err)
	}
	unconfirmed, err := q.HasUnconfirmedContainerCleanup(ctx, source.ID)
	if err != nil {
		return DeploymentResult{}, err
	}
	if unconfirmed != 0 {
		return DeploymentResult{}, ErrContainerCleanupUnconfirmed
	}
	if source.ReleaseID != arg.ReleaseID {
		return DeploymentResult{}, errors.New(
			"source deployment release mismatch",
		)
	}
	stepSource, err := q.GetDeploymentStepSource(ctx, sourceDeploymentID)
	if errors.Is(err, sql.ErrNoRows) {
		release, releaseErr := q.GetRelease(ctx, arg.ReleaseID)
		if releaseErr != nil {
			return DeploymentResult{}, fmt.Errorf(
				"get source release steps: %w",
				releaseErr,
			)
		}
		stepSource.StepsJson = release.StepsJson
	} else if err != nil {
		return DeploymentResult{}, fmt.Errorf(
			"get source deployment steps: %w",
			err,
		)
	}
	steps, err := deploymentStepsFromRelease(stepSource.StepsJson)
	if err != nil {
		return DeploymentResult{}, err
	}
	result, err := r.createDeploymentWithSteps(
		ctx,
		q,
		arg,
		steps,
		stepSource.StepsJson,
	)
	if err != nil {
		return DeploymentResult{}, err
	}
	if err := q.CopyDeploymentArtifact(ctx, db.CopyDeploymentArtifactParams{
		DeploymentID: result.Deployment.ID, DeploymentID_2: sourceDeploymentID,
	}); err != nil {
		return DeploymentResult{}, err
	}
	return result, validateDeploymentArtifact(
		ctx,
		q,
		result.Deployment.ID,
		steps,
	)
}

func (r *Repository) createDeployment(
	ctx context.Context,
	q *db.Queries,
	arg db.CreateDeploymentParams,
) (DeploymentResult, error) {
	if _, err := q.LockDeploymentEnvironment(
		ctx,
		arg.EnvironmentID,
	); err != nil {
		return DeploymentResult{}, err
	}
	if err := lockRelease(ctx, q, arg.ReleaseID); err != nil {
		return DeploymentResult{}, err
	}
	release, err := q.GetRelease(ctx, arg.ReleaseID)
	if err != nil {
		return DeploymentResult{}, fmt.Errorf("get release: %w", err)
	}
	steps, err := deploymentStepsFromRelease(release.StepsJson)
	if err != nil {
		return DeploymentResult{}, err
	}
	result, err := r.createDeploymentWithSteps(ctx, q, arg, steps, "")
	if err != nil {
		return result, err
	}
	if err := q.CopyReleaseArtifactToDeployment(
		ctx,
		db.CopyReleaseArtifactToDeploymentParams{
			DeploymentID: result.Deployment.ID,
			ReleaseID:    arg.ReleaseID,
		},
	); err != nil {
		return DeploymentResult{}, err
	}
	return result, validateDeploymentArtifact(
		ctx,
		q,
		result.Deployment.ID,
		steps,
	)
}

func (r *Repository) createDeploymentWithSteps(
	ctx context.Context,
	q *db.Queries,
	arg db.CreateDeploymentParams,
	steps []DeploymentStepSnapshot,
	stepsJSON string,
) (DeploymentResult, error) {
	releaseKind, err := q.GetRelease(ctx, arg.ReleaseID)
	if err != nil {
		return DeploymentResult{}, err
	}
	variables, err := q.ListReleaseVariablesByRelease(ctx, arg.ReleaseID)
	if err != nil {
		return DeploymentResult{}, err
	}
	for _, variable := range variables {
		if variable.Name == containerenv.StageVariable ||
			variable.Name == containerenv.ApprovedVariable {
			return DeploymentResult{}, containerenv.ErrReserved
		}
	}
	gated := false
	for _, step := range steps {
		if step.ApprovalArtifactPath != "" {
			gated = true
		}
	}
	if gated {
		if releaseKind.Kind != "deployment" {
			return DeploymentResult{}, artifact.ErrGateConfig
		}
		for i, step := range steps {
			if step.ExecutionTarget != "local" || step.MaxRetries != 0 {
				return DeploymentResult{}, artifact.ErrGateConfig
			}
			if i == len(steps)-1 && step.ApprovalArtifactPath != "" {
				return DeploymentResult{}, artifact.ErrGateConfig
			}
		}

	}
	arg.AssignedAgentID = sql.NullString{}
	if arg.Status == "pending" {
		arg.Status = "queued"
	}
	deployment, err := q.CreateDeployment(ctx, arg)
	if err != nil {
		return DeploymentResult{}, fmt.Errorf("insert deployment: %w", err)
	}
	if err := r.snapshotDeploymentVariables(ctx, q, deployment); err != nil {
		return DeploymentResult{}, err
	}
	if gated {
		if err := q.CreateArtifactGateRun(ctx, deployment.ID); err != nil {
			return DeploymentResult{}, err
		}
	}
	steps, err = snapshotVerification(ctx, q, deployment, steps)
	if err != nil {
		return DeploymentResult{}, err
	}
	if err := snapshotDeploymentStepsFromSource(
		ctx,
		q,
		deployment.ID,
		steps,
		stepsJSON,
	); err != nil {
		return DeploymentResult{}, fmt.Errorf("snapshot release steps: %w", err)
	}
	queueAudit(ctx, deployment.ID, "deployment_queue_created")
	if err := advanceEnvironmentQueue(ctx, q, arg.EnvironmentID); err != nil {
		return DeploymentResult{}, err
	}
	deployment, err = q.GetDeployment(ctx, deployment.ID)
	return DeploymentResult{Deployment: deployment, Mode: ExecutionLocal}, err
}

package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func (r *DeploymentRunner) Run(
	ctx context.Context,
	deploymentID, releaseID, environmentID int64,
) {
	runCtx, cancel := context.WithCancel(ctx)
	r.RegisterCancel(deploymentID, cancel)
	defer r.UnregisterCancel(deploymentID)

	_ = r.repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID:        deploymentID,
			Status:    "running",
			StartedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	)
	release, err := r.repo.Queries.GetRelease(ctx, releaseID)
	if err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}

	environmentName := "unknown environment"
	if environment, getErr := r.repo.Queries.GetEnvironment(
		ctx,
		environmentID,
	); getErr == nil {
		environmentName = environment.Name
	}
	r.publish(
		ctx,
		events.DeploymentStarted,
		deploymentID,
		release.ProjectID,
		environmentID,
		fmt.Sprintf(
			"Deployment #%d started on %s",
			deploymentID,
			environmentName,
		),
	)

	var steps []Step
	if err := json.Unmarshal([]byte(release.StepsJson), &steps); err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}
	variables, err := r.repo.ListReleaseVariablesByRelease(ctx, releaseID)
	if err != nil {
		_ = r.failUnlessCancelled(ctx, deploymentID)
		return
	}
	environment, secrets := releaseEnvironment(variables, environmentID)

	err = r.executor.ExecuteSteps(runCtx, ExecutionConfig{
		DeploymentID: deploymentID,
		Steps:        steps,
		Environment:  environment,
		Secrets:      secrets,
		CallbacksForStep: func(step Step) Callbacks {
			return r.callbacks(ctx, deploymentID, step.Name)
		},
	})
	if errors.Is(err, ErrCancelled) {
		return
	}
	if err != nil {
		_ = r.repo.Queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:     deploymentID,
				Status: "failed",
				FinishedAt: sql.NullInt64{
					Int64: time.Now().Unix(),
					Valid: true,
				},
			},
		)
		r.publish(
			ctx,
			events.DeploymentFailed,
			deploymentID,
			release.ProjectID,
			environmentID,
			fmt.Sprintf(
				"Deployment #%d failed on %s: %v",
				deploymentID,
				environmentName,
				err,
			),
		)
		return
	}

	deployment, _ := r.repo.Queries.GetDeployment(ctx, deploymentID)
	if deployment.Status == "cancelled" {
		return
	}
	_ = r.repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID:         deploymentID,
			Status:     "succeeded",
			FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	)
	r.publish(
		ctx,
		events.DeploymentSucceeded,
		deploymentID,
		release.ProjectID,
		environmentID,
		fmt.Sprintf(
			"Deployment #%d succeeded on %s",
			deploymentID,
			environmentName,
		),
	)
}

func (r *DeploymentRunner) callbacks(
	ctx context.Context,
	deploymentID int64,
	stepName string,
) Callbacks {
	return NewCallbacks(CallbacksConfig{
		WriteLog: func(line string) error {
			log, err := r.repo.Queries.CreateDeploymentLog(
				ctx,
				db.CreateDeploymentLogParams{
					DeploymentID: deploymentID,
					StepName:     sql.NullString{String: stepName, Valid: true},
					Line:         line,
				},
			)
			if err != nil {
				return fmt.Errorf("persist deployment log: %w", err)
			}
			r.broker.Broadcast(deploymentID, log.ID)
			return nil
		},
		TrackProcessGroup:   func(pgid int) { r.trackProcessGroup(deploymentID, pgid) },
		UntrackProcessGroup: func() { r.untrackProcessGroup(deploymentID) },
		Cancelled: func() bool {
			deployment, _ := r.repo.Queries.GetDeployment(ctx, deploymentID)
			return deployment.Status == "cancelled"
		},
	})
}

func releaseEnvironment(
	variables []db.ReleaseVariable,
	environmentID int64,
) (map[string]string, []string) {
	environment := make(map[string]string)
	var secrets []string
	for _, variable := range ResolveReleaseVariables(variables, environmentID) {
		environment[variable.Name] = variable.Value.String
		if variable.Secret != 0 && variable.Value.String != "" {
			secrets = append(secrets, variable.Value.String)
		}
	}
	return environment, secrets
}

func (r *DeploymentRunner) failUnlessCancelled(
	ctx context.Context,
	deploymentID int64,
) error {
	deployment, _ := r.repo.Queries.GetDeployment(ctx, deploymentID)
	if deployment.Status == "cancelled" {
		return nil
	}
	return r.repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID:         deploymentID,
			Status:     "failed",
			FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	)
}

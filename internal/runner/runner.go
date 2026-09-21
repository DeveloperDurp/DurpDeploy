package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/interpreter"
	"durpdeploy/internal/repository"
)

const (
	defaultStepTimeout = 5 * time.Minute
	serviceUsername    = "durpdeploy"
)

type DeploymentRunner struct {
	repo    *repository.Repository
	broker  *LogBroker
	mu      sync.Mutex
	cancels map[int64]context.CancelFunc
	// pgids tracks the process group of every step currently executing,
	// keyed by deployment ID. Populated in runStepAttempt, cleared when
	// the step exits. Used by KillAll to reap orphans on server shutdown.
	// ponytail: one entry per deployment (steps run sequentially, no
	// parallel step execution), so a plain map is enough.
	pgids      map[int64]int
	sandboxErr error
	// bus publishes deployment_started/succeeded/failed events for the
	// Slack/email notifiers (Stage 3). Nil until SetEventBus is called —
	// existing callers (tests, recovery path) that never call it simply
	// get no notifications, no other behavior change.
	bus *events.Bus
}

type deploymentStep struct {
	Name            string   `json:"name"`
	ScriptBody      string   `json:"script_body"`
	Interpreter     string   `json:"interpreter"`
	SortOrder       int64    `json:"sort_order"`
	TimeoutSeconds  int64    `json:"timeout_seconds"`
	MaxRetries      int64    `json:"max_retries"`
	ExecutionTarget string   `json:"execution_target"`
	AgentSelectors  []string `json:"agent_selectors"`
}

func New(repo *repository.Repository, broker *LogBroker) *DeploymentRunner {
	sandboxErr := validateExecutionBoundary()
	return &DeploymentRunner{
		repo:       repo,
		broker:     broker,
		cancels:    make(map[int64]context.CancelFunc),
		pgids:      make(map[int64]int),
		sandboxErr: sandboxErr,
	}
}

func (r *DeploymentRunner) Run(
	ctx context.Context,
	deploymentID, releaseID, environmentID int64,
) {
	runCtx, cancel := context.WithCancel(ctx)
	r.RegisterCancel(deploymentID, cancel)

	now := time.Now().Unix()

	_ = r.repo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{
			ID:        deploymentID,
			Status:    "running",
			StartedAt: sql.NullInt64{Int64: now, Valid: true},
		},
	)

	release, err := r.repo.Queries.GetRelease(ctx, releaseID)
	if err != nil {
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return
	}

	envName := "unknown environment"
	if env, err := r.repo.Queries.GetEnvironment(
		ctx,
		environmentID,
	); err == nil {
		envName = env.Name
	}
	r.publish(ctx, events.Event{
		Type:          events.DeploymentStarted,
		DeploymentID:  deploymentID,
		ProjectID:     release.ProjectID,
		EnvironmentID: environmentID,
		Message: fmt.Sprintf(
			"Deployment #%d started on %s",
			deploymentID,
			envName,
		),
	})

	var steps []deploymentStep
	stepSource, err := r.repo.Queries.GetDeploymentStepSource(ctx, deploymentID)
	if err != nil {
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return
	}
	if err := json.Unmarshal([]byte(stepSource.StepsJson), &steps); err != nil {
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return
	}

	vars, err := r.repo.ListReleaseVariablesByRelease(ctx, releaseID)
	if err != nil {
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return
	}

	resolved, err := ResolveReleaseVariables(vars, environmentID)
	if err != nil {
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return
	}
	envMap := make(map[string]string, len(resolved))
	var secretValues []string
	for _, variable := range resolved {
		envMap[variable.Name] = variable.Value
		if variable.Secret && variable.Value != "" {
			secretValues = append(secretValues, variable.Value)
		}
	}

	scrubber := NewScrubber(secretValues)

	for stepIndex, step := range steps {
		if runCtx.Err() != nil {
			r.finalizeCancellation(ctx, deploymentID)
			return
		}
		logWriter := &broadcastWriter{
			broker:       r.broker,
			repo:         r.repo,
			deploymentID: deploymentID,
			stepName:     step.Name,
			ctx:          ctx,
			scrubber:     scrubber,
		}
		if step.ExecutionTarget == "agent" {
			if step.Interpreter != "" && step.Interpreter != interpreter.Bash {
				_, _ = logWriter.Write([]byte(fmt.Sprintf(
					"step %q: agent execution does not support interpreter %q\n",
					step.Name,
					step.Interpreter,
				)))
				logWriter.Flush()
				r.failStep(ctx, runCtx, events.Event{
					Type: events.DeploymentFailed, DeploymentID: deploymentID,
					ProjectID: release.ProjectID, EnvironmentID: environmentID,
					Message: fmt.Sprintf(
						"Deployment #%d failed on %s: agent execution does not support interpreter %q",
						deploymentID,
						envName,
						step.Interpreter,
					),
				}, false)
				return
			}
			err := r.runRemoteStep(ctx, runCtx, remoteStepRequest{
				deploymentID: deploymentID,
				stepIndex:    int64(stepIndex),
				step:         step,
				logWriter:    logWriter,
			})
			if err != nil {
				if errors.Is(err, errDeploymentCancelled) {
					r.finalizeCancellation(ctx, deploymentID)
					return
				}
				r.failStep(ctx, runCtx, events.Event{
					Type: events.DeploymentFailed, DeploymentID: deploymentID,
					ProjectID: release.ProjectID, EnvironmentID: environmentID,
					Message: fmt.Sprintf(
						"Deployment #%d failed on %s: %v",
						deploymentID, envName, err,
					),
				}, false)
				return
			}
			continue
		}

		var lastErr error
		maxAttempts := int(step.MaxRetries) + 1
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			lastErr = r.runStepAttempt(runCtx, localStepAttempt{
				deploymentID: deploymentID,
				step:         step,
				logWriter:    logWriter,
				environment:  envMap,
				attempt:      attempt,
			})
			if lastErr == nil {
				break
			}
			if runCtx.Err() != nil {
				r.finalizeCancellation(ctx, deploymentID)
				return
			}

			dep, _ := r.repo.Queries.GetDeployment(ctx, deploymentID)
			if dep.Status == "cancelled" {
				return
			}

			if attempt < maxAttempts {
				logWriter.Write(
					[]byte(
						fmt.Sprintf(
							"step %q: retrying (attempt %d of %d)\n",
							step.Name,
							attempt+1,
							maxAttempts,
						),
					),
				)
				logWriter.Flush()
			}
		}

		if lastErr != nil {
			r.failStep(ctx, runCtx, events.Event{
				Type: events.DeploymentFailed, DeploymentID: deploymentID,
				ProjectID: release.ProjectID, EnvironmentID: environmentID,
				Message: fmt.Sprintf(
					"Deployment #%d failed on %s: %v",
					deploymentID, envName, lastErr,
				),
			}, true)
			return
		}
	}

	if runCtx.Err() != nil {
		r.finalizeCancellation(ctx, deploymentID)
		return
	}
	status, persisted := r.persistCompletion(
		ctx, runCtx, deploymentID, "succeeded", true,
	)
	if !persisted {
		return
	}
	if status != "succeeded" {
		return
	}
	r.publish(ctx, events.Event{
		Type:          events.DeploymentSucceeded,
		DeploymentID:  deploymentID,
		ProjectID:     release.ProjectID,
		EnvironmentID: environmentID,
		Message: fmt.Sprintf(
			"Deployment #%d succeeded on %s",
			deploymentID,
			envName,
		),
	})
}

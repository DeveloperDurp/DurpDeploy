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
	"durpdeploy/internal/repository"
)

const defaultStepTimeout = 5 * time.Minute

type DeploymentRunner struct {
	repo      *repository.Repository
	broker    *LogBroker
	mu        sync.Mutex
	cancels   map[int64]context.CancelFunc
	attempts  map[int64]string
	engine    containerEndpoint
	localErr  error
	staging   map[int64][]artifactStage
	stopping  bool
	localWork sync.WaitGroup
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
	ContainerImage  string   `json:"container_image"`
	VariableNames   []string `json:"variable_names"`
}

func New(repo *repository.Repository, broker *LogBroker) *DeploymentRunner {
	return newRunner(repo, broker, "")
}

func (r *DeploymentRunner) ContainerRuntimeReady() bool {
	return r.localErr == nil
}

func (r *DeploymentRunner) Run(
	ctx context.Context,
	deploymentID, releaseID, environmentID int64,
) {
	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		cancel()
		return
	}
	r.broker.beginDeployment(deploymentID)
	defer r.broker.endDeployment(deploymentID)
	r.cancels[deploymentID] = cancel
	r.mu.Unlock()

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
	if !r.beginLocalWork() {
		r.finalizeCancellation(ctx, deploymentID)
		return
	}
	artifactWork := true
	defer func() {
		if artifactWork {
			r.localWork.Done()
		}
	}()
	stage, err := r.stageArtifact(runCtx, deploymentID)
	if err != nil {
		r.failStep(
			ctx,
			runCtx,
			events.Event{
				Type:          events.DeploymentFailed,
				DeploymentID:  deploymentID,
				ProjectID:     release.ProjectID,
				EnvironmentID: environmentID,
				Message:       "Artifact staging failed: " + err.Error(),
			},
			true,
		)
		return
	}
	var handoff artifactStage
	if stage.volume == "" {
		r.localWork.Done()
		artifactWork = false
	}
	if stage.volume != "" {
		if _, exists := envMap["ARTIFACT_PATH"]; exists {
			r.failStep(
				ctx,
				runCtx,
				events.Event{
					Type:          events.DeploymentFailed,
					DeploymentID:  deploymentID,
					ProjectID:     release.ProjectID,
					EnvironmentID: environmentID,
					Message:       "ARTIFACT_PATH conflicts with a snapshotted variable",
				},
				true,
			)
			return
		}
		envMap["ARTIFACT_PATH"] = artifactMount
	}
	defer func() {
		if err := r.cleanupArtifact(deploymentID); err != nil {
			r.persistCompletion(
				ctx,
				runCtx,
				deploymentID,
				"cleanup_unconfirmed",
				false,
			)
		}
	}()

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
			stepIndex:    sql.NullInt64{Int64: int64(stepIndex), Valid: true},
			ctx:          ctx,
			scrubber:     scrubber,
		}
		switch step.ExecutionTarget {
		case "agent":
			// Shutdown drains local resources without waiting for agent ACKs.
			if artifactWork {
				r.localWork.Done()
				artifactWork = false
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
		case "", "local":
			if !artifactWork {
				if !r.beginLocalWork() {
					r.finalizeCancellation(ctx, deploymentID)
					return
				}
				artifactWork = true
			}
		default:
			r.failStep(ctx, runCtx, events.Event{
				Type:          events.DeploymentFailed,
				DeploymentID:  deploymentID,
				ProjectID:     release.ProjectID,
				EnvironmentID: environmentID,
				Message: fmt.Sprintf(
					"Deployment #%d failed: unknown execution target %q",
					deploymentID,
					step.ExecutionTarget,
				),
			}, true)
			return
		}

		var lastErr error
		namespace := sql.NullString{String: r.engine.scope(), Valid: true}
		recorded, err := r.repo.Queries.RecordContainerNamespace(ctx,
			db.RecordContainerNamespaceParams{
				DeploymentID: deploymentID, Namespace: namespace,
			})
		if err != nil {
			r.failStep(ctx, runCtx, events.Event{
				Type:          events.DeploymentFailed,
				DeploymentID:  deploymentID,
				ProjectID:     release.ProjectID,
				EnvironmentID: environmentID,
				Message: fmt.Sprintf(
					"Deployment #%d failed before container execution: %v",
					deploymentID,
					err,
				),
			}, true)
			return
		}
		if recorded != 1 {
			deployment, loadErr := r.repo.Queries.GetDeployment(
				ctx, deploymentID,
			)
			if loadErr == nil && deployment.ContainerNamespace.Valid {
				r.persistCompletion(ctx, runCtx, deploymentID,
					"cleanup_unconfirmed", false)
				return
			}
			if loadErr == nil && deployment.Status == "cancelled" {
				return
			}
			r.failStep(ctx, runCtx, events.Event{
				Type:          events.DeploymentFailed,
				DeploymentID:  deploymentID,
				ProjectID:     release.ProjectID,
				EnvironmentID: environmentID,
				Message: fmt.Sprintf(
					"Deployment #%d failed before container execution: container namespace was not recorded",
					deploymentID,
				),
			}, true)
			return
		}
		if handoff.volume == "" {
			handoff, err = r.stageDeployment(runCtx, deploymentID, envMap)
			if err != nil {
				_, writeErr := logWriter.Write([]byte(fmt.Sprintf(
					"step %q: staging failed before execution: %v\n",
					step.Name,
					err,
				)))
				logWriter.Flush()
				logWriter.finishState(err, runCtx.Err() != nil)
				r.failStep(ctx, runCtx, events.Event{
					Type: events.DeploymentFailed, DeploymentID: deploymentID,
					ProjectID: release.ProjectID, EnvironmentID: environmentID,
					Message: "Deployment staging failed: " + errors.Join(err, writeErr).
						Error(),
				}, true)
				return
			}
		}
		maxAttempts := int(step.MaxRetries) + 1
		for attempt := 1; attempt <= maxAttempts; attempt++ {
			lastErr = r.runStepAttempt(runCtx, localStepAttempt{
				deploymentID: deploymentID,
				step:         step,
				logWriter:    logWriter,
				environment:  envMap,
				attempt:      attempt,
				artifact:     stage,
				handoff:      handoff,
			})
			if lastErr == nil {
				break
			}
			if errors.Is(lastErr, errContainerCleanup) {
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
			if errors.Is(lastErr, errContainerCleanup) {
				r.persistCompletion(ctx, runCtx, deploymentID,
					"cleanup_unconfirmed", false)
				return
			}
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
	if err := r.verifyDeployment(ctx, runCtx, deploymentID,
		envMap, secretValues, stage); err != nil {
		if errors.Is(err, errContainerCleanup) {
			r.persistCompletion(ctx, runCtx, deploymentID,
				"cleanup_unconfirmed", false)
			return
		}
		if errors.Is(err, errDeploymentCancelled) ||
			(errors.Is(err, context.Canceled) && runCtx.Err() != nil) {
			r.finalizeCancellation(ctx, deploymentID)
			return
		}
		r.failStep(ctx, runCtx, events.Event{
			Type:          events.DeploymentFailed,
			DeploymentID:  deploymentID,
			ProjectID:     release.ProjectID,
			EnvironmentID: environmentID,
			Message: fmt.Sprintf(
				"Deployment #%d failed on %s: verification failed",
				deploymentID,
				envName,
			),
		}, runCtx.Err() == nil)
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

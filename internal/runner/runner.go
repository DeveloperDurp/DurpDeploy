package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	Name                 string   `json:"name"`
	ScriptBody           string   `json:"script_body"`
	Interpreter          string   `json:"interpreter"`
	SortOrder            int64    `json:"sort_order"`
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
	r.mu.Lock()
	stopping := r.stopping
	r.mu.Unlock()
	if stopping {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		cancel()
		return
	}
	if _, exists := r.cancels[deploymentID]; exists {
		r.mu.Unlock()
		cancel()
		return
	}
	gateRun, gated, gateErr := r.repo.BeginArtifactGateRun(ctx, deploymentID)
	if gateErr != nil {
		r.mu.Unlock()
		cancel()
		if !errors.Is(gateErr, repository.ErrArtifactGate) {
			slog.Error(
				"claim deployment run",
				"deployment_id",
				deploymentID,
				"err",
				gateErr,
			)
			r.failUnlessCancelled(context.WithoutCancel(ctx), ctx, deploymentID)
		}
		return
	}
	if !gated {
		started, err := r.repo.StartLocalDeployment(ctx, deploymentID)
		if err != nil || !started {
			r.mu.Unlock()
			cancel()
			if err != nil {
				slog.Error(
					"claim deployment",
					"deployment_id",
					deploymentID,
					"err",
					err,
				)
			}
			return
		}
	}
	r.broker.beginDeployment(deploymentID)
	defer r.broker.endDeployment(deploymentID)
	r.cancels[deploymentID] = cancel
	r.mu.Unlock()

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
	if !gated || gateRun.NextStep == 0 {
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
	}

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

	vars, err := r.repo.ListDeploymentVariables(ctx, deploymentID)
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
	if gated {
		if err := r.pinArtifactGateImages(
			runCtx,
			deploymentID,
			steps,
		); err != nil {
			r.failUnlessCancelled(ctx, runCtx, deploymentID)
			return
		}
	}
	stage, err := r.stageArtifact(runCtx, deploymentID)
	if err != nil {
		message := "Artifact staging failed: " + err.Error()
		writer := &broadcastWriter{
			broker: r.broker, repo: r.repo, deploymentID: deploymentID,
			ctx: ctx, scrubber: scrubber,
		}
		if _, logErr := writer.Write([]byte(message + "\n")); logErr != nil {
			slog.Error("persist artifact pull failure", "error", logErr)
		}
		writer.Flush()
		r.failStep(
			ctx,
			runCtx,
			events.Event{
				Type:          events.DeploymentFailed,
				DeploymentID:  deploymentID,
				ProjectID:     release.ProjectID,
				EnvironmentID: environmentID,
				Message:       message,
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
	cleanupDone := false
	defer func() {
		if cleanupDone {
			return
		}
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

	var approved artifactStage
	if gated && gateRun.NextStep > 0 {
		approved, err = r.restoreArtifactGate(
			runCtx,
			deploymentID,
			gateRun.NextStep-1,
		)
		if err != nil {
			r.failUnlessCancelled(ctx, runCtx, deploymentID)
			return
		}
	}

	for stepIndex, step := range steps {
		if gated && int64(stepIndex) < gateRun.NextStep {
			continue
		}
		if gated {
			imageID, err := r.repo.Queries.GetArtifactGateImage(
				ctx,
				db.GetArtifactGateImageParams{
					DeploymentID: deploymentID,
					StepIndex:    int64(stepIndex),
				},
			)
			if err != nil {
				r.failUnlessCancelled(ctx, runCtx, deploymentID)
				return
			}
			step.ContainerImage = imageID
		}
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
				if errors.Is(err, errContainerCleanup) {
					r.persistCompletion(ctx, runCtx, deploymentID,
						"cleanup_unconfirmed", false)
					return
				}
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
					Type:          events.DeploymentFailed,
					DeploymentID:  deploymentID,
					ProjectID:     release.ProjectID,
					EnvironmentID: environmentID,
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
				approved:     approved,
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
		if step.ApprovalArtifactPath != "" {
			err := r.captureArtifactGate(
				runCtx,
				deploymentID,
				int64(stepIndex),
				step,
				handoff,
			)
			if err == nil {
				err = r.cleanupArtifact(deploymentID)
				cleanupDone = err == nil
			}
			if err == nil {
				r.mu.Lock()
				if runCtx.Err() != nil {
					err = runCtx.Err()
				} else {
					err = r.repo.PauseArtifactGate(
						ctx,
						deploymentID,
						int64(stepIndex)+1,
					)
					if err == nil {
						delete(r.cancels, deploymentID)
					}
				}
				r.mu.Unlock()
			}
			if err != nil {
				r.failUnlessCancelled(ctx, runCtx, deploymentID)
				return
			}
			cancel()
			r.publish(
				ctx,
				events.Event{
					Type:          events.ArtifactAwaitingApproval,
					DeploymentID:  deploymentID,
					ProjectID:     release.ProjectID,
					EnvironmentID: environmentID,
					Message:       "Deployment artifact is ready for approval",
				},
			)
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

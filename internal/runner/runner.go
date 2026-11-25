package runner

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/containerenv"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/logscrub"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/stepdefinition"
)

type DeploymentRunner struct {
	repo              *repository.Repository
	broker            *LogBroker
	mu                sync.Mutex
	cancels           map[int64]context.CancelFunc
	attempts          map[int64]string
	engine            containerEndpoint
	localErr          error
	staging           map[int64][]artifactStage
	transfers         map[int64]*os.File
	stopping          bool
	localWork         sync.WaitGroup
	completionWork    sync.WaitGroup
	completionCancels map[int64]context.CancelFunc
	// bus publishes deployment_started/succeeded/failed events for the
	// Slack/email notifiers (Stage 3). Nil until SetEventBus is called —
	// existing callers (tests, recovery path) that never call it simply
	// get no notifications, no other behavior change.
	bus *events.Bus
}

type deploymentStep stepdefinition.Definition

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

	r.runClaimedDeployment(ctx, runCtx, cancel, deploymentRunClaim{
		deploymentID:  deploymentID,
		releaseID:     releaseID,
		environmentID: environmentID,
		gateRun:       gateRun,
		gated:         gated,
	})
}

type deploymentRunClaim struct {
	deploymentID, releaseID, environmentID int64
	gateRun                                db.ArtifactGateRun
	gated                                  bool
}

// deploymentRun is owned by runClaimedDeployment for one invocation.
type deploymentRun struct {
	deploymentRunClaim
	projectID                   int64
	envName                     string
	steps                       []deploymentStep
	environment                 map[string]string
	secretValues                []string
	scrubber                    *logscrub.Scrubber
	artifact, handoff, approved artifactStage
	transferred                 *os.File
	artifactWork, cleanupDone   bool
}

func (r *DeploymentRunner) runClaimedDeployment(
	ctx, runCtx context.Context,
	cancel context.CancelFunc,
	claim deploymentRunClaim,
) {
	deploymentID := claim.deploymentID
	run, ok := r.loadDeploymentRun(ctx, runCtx, claim)
	if !ok {
		return
	}
	if !r.beginLocalWork() {
		r.finalizeCancellation(ctx, deploymentID)
		return
	}
	run.artifactWork = true
	defer func() {
		if run.artifactWork {
			r.localWork.Done()
		}
	}()
	if !r.prepareRunArtifact(ctx, runCtx, run) {
		return
	}
	defer func() {
		if run.cleanupDone {
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

	if !r.restoreRunApproval(ctx, runCtx, run) {
		return
	}
	if !r.runDeploymentSteps(ctx, runCtx, cancel, run) {
		return
	}
	r.finishDeploymentRun(ctx, runCtx, run)
}

func (r *DeploymentRunner) loadDeploymentRun(
	ctx, runCtx context.Context,
	claim deploymentRunClaim,
) (*deploymentRun, bool) {
	deploymentID, releaseID, environmentID := claim.deploymentID,
		claim.releaseID, claim.environmentID
	deployment, err := r.repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil || deployment.ReleaseID != releaseID ||
		deployment.EnvironmentID != environmentID {
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return nil, false
	}
	release, err := r.repo.Queries.GetRelease(ctx, releaseID)
	if err != nil {
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return nil, false
	}

	run := &deploymentRun{
		deploymentRunClaim: claim,
		projectID:          release.ProjectID,
		envName:            deployment.EnvironmentNameSnapshot.String,
	}
	if !claim.gated || claim.gateRun.NextStep == 0 {
		r.publish(ctx, events.Event{
			Type:          events.DeploymentStarted,
			DeploymentID:  deploymentID,
			ProjectID:     release.ProjectID,
			EnvironmentID: environmentID,
			Message: fmt.Sprintf(
				"Deployment #%d started on %s",
				deploymentID,
				run.envName,
			),
		})
	}

	run.steps, err = r.loadDeploymentSteps(
		ctx, deploymentID, deployment.AnsibleCheckMode,
	)
	if err != nil {
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return nil, false
	}
	if !r.loadDeploymentEnvironment(ctx, runCtx, run, deployment) {
		return nil, false
	}
	run.scrubber = r.repo.NewScrubber(run.secretValues)
	return run, true
}

func (r *DeploymentRunner) loadDeploymentSteps(
	ctx context.Context,
	deploymentID int64,
	ansibleCheckMode sql.NullInt64,
) ([]deploymentStep, error) {
	var steps []deploymentStep
	stepSource, err := r.repo.Queries.GetDeploymentStepSource(ctx, deploymentID)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(stepSource.StepsJson), &steps); err != nil {
		return nil, err
	}
	if ansibleCheckMode.Valid {
		for i := range steps {
			if steps[i].Ansible != nil {
				mode := ansibleCheckMode.Int64 == 1
				steps[i].Ansible.Check = &mode
			}
		}
	}
	for _, step := range steps {
		if err := agentexecution.ValidateLimitsWithPolicy(step.TimeoutSeconds, step.MaxRetries, r.repo.ResourceLimits().Execution); err != nil {
			return nil, err
		}
	}
	return steps, nil
}

func (r *DeploymentRunner) loadDeploymentEnvironment(
	ctx, runCtx context.Context,
	run *deploymentRun,
	deployment db.Deployment,
) bool {
	deploymentID := run.deploymentID
	vars, err := r.repo.ListDeploymentVariables(ctx, deploymentID)
	if err != nil {
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return false
	}

	resolved, err := ResolveDeploymentVariables(vars, deployment)
	if err != nil {
		if errors.Is(err, containerenv.ErrEnvironment) {
			writer := &broadcastWriter{
				broker: r.broker, repo: r.repo, deploymentID: deploymentID,
				ctx: ctx, scrubber: r.repo.NewScrubber(nil),
			}
			if _, logErr := writer.Write([]byte(err.Error() + "\n")); logErr != nil {
				slog.Error(
					"persist environment validation failure",
					"error",
					logErr,
				)
			}
			writer.Flush()
		}
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return false
	}
	run.environment = make(map[string]string, len(resolved))
	for _, variable := range resolved {
		run.environment[variable.Name] = variable.Value
		if variable.Secret && variable.Value != "" {
			run.secretValues = append(run.secretValues, variable.Value)
		}
	}
	return true
}

func (r *DeploymentRunner) prepareRunArtifact(
	ctx, runCtx context.Context,
	run *deploymentRun,
) bool {
	deploymentID := run.deploymentID
	projectID, environmentID := run.projectID, run.environmentID
	scrubber := run.scrubber
	if run.gated {
		if err := r.pinArtifactGateImages(
			runCtx,
			deploymentID,
			run.steps,
		); err != nil {
			r.failUnlessCancelled(ctx, runCtx, deploymentID)
			return false
		}
	}
	var err error
	run.artifact, err = r.stageArtifact(runCtx, deploymentID)
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
				ProjectID:     projectID,
				EnvironmentID: environmentID,
				Message:       scrubber.Scrub(message),
			},
			true,
		)
		return false
	}
	if run.artifact.volume == "" {
		r.localWork.Done()
		run.artifactWork = false
	}
	if run.artifact.volume != "" {
		if _, exists := run.environment["ARTIFACT_PATH"]; exists {
			r.failStep(
				ctx,
				runCtx,
				events.Event{
					Type:          events.DeploymentFailed,
					DeploymentID:  deploymentID,
					ProjectID:     projectID,
					EnvironmentID: environmentID,
					Message:       "ARTIFACT_PATH conflicts with a snapshotted variable",
				},
				true,
			)
			return false
		}
		run.environment["ARTIFACT_PATH"] = artifactMount
	}
	return true
}

func (r *DeploymentRunner) restoreRunApproval(
	ctx, runCtx context.Context,
	run *deploymentRun,
) bool {
	deploymentID := run.deploymentID
	if run.gated && run.gateRun.NextStep > 0 {
		if !run.artifactWork {
			if !r.beginLocalWork() {
				r.finalizeCancellation(ctx, deploymentID)
				return false
			}
			run.artifactWork = true
		}
		var err error
		run.approved, err = r.restoreArtifactGate(
			runCtx,
			deploymentID,
			run.gateRun.NextStep-1,
		)
		if err != nil {
			r.failUnlessCancelled(ctx, runCtx, deploymentID)
			return false
		}
	}
	return true
}

func (r *DeploymentRunner) runDeploymentSteps(
	ctx, runCtx context.Context,
	cancel context.CancelFunc,
	run *deploymentRun,
) bool {
	deploymentID := run.deploymentID
	for stepIndex, step := range run.steps {
		if run.gated && int64(stepIndex) < run.gateRun.NextStep {
			continue
		}
		if run.gated && step.ExecutionTarget != "agent" {
			imageID, err := r.repo.Queries.GetArtifactGateImage(
				ctx,
				db.GetArtifactGateImageParams{
					DeploymentID: deploymentID,
					StepIndex:    int64(stepIndex),
				},
			)
			if err != nil {
				r.failUnlessCancelled(ctx, runCtx, deploymentID)
				return false
			}
			step.ContainerImage = imageID
		}
		if runCtx.Err() != nil {
			r.finalizeCancellation(ctx, deploymentID)
			return false
		}
		if !r.runDeploymentStep(ctx, runCtx, cancel, run, stepIndex, step) {
			return false
		}
	}
	return true
}

func (r *DeploymentRunner) runDeploymentStep(
	ctx, runCtx context.Context,
	cancel context.CancelFunc,
	run *deploymentRun,
	stepIndex int,
	step deploymentStep,
) bool {
	deploymentID := run.deploymentID
	logWriter := &broadcastWriter{
		broker:       r.broker,
		repo:         r.repo,
		deploymentID: deploymentID,
		stepName:     step.Name,
		stepIndex:    sql.NullInt64{Int64: int64(stepIndex), Valid: true},
		ctx:          ctx,
		scrubber:     run.scrubber,
	}
	switch step.ExecutionTarget {
	case "agent":
		return r.runRemoteDeploymentStep(
			ctx,
			runCtx,
			run,
			stepIndex,
			step,
			logWriter,
		)
	case "", "local":
		if !run.artifactWork {
			if !r.beginLocalWork() {
				r.finalizeCancellation(ctx, deploymentID)
				return false
			}
			run.artifactWork = true
		}
	default:
		r.failStep(ctx, runCtx, events.Event{
			Type:          events.DeploymentFailed,
			DeploymentID:  deploymentID,
			ProjectID:     run.projectID,
			EnvironmentID: run.environmentID,
			Message: fmt.Sprintf(
				"Deployment #%d failed: unknown execution target %q",
				deploymentID,
				step.ExecutionTarget,
			),
		}, true)
		return false
	}

	if !r.runLocalDeploymentStep(ctx, runCtx, run, step, logWriter) {
		return false
	}
	if step.ApprovalArtifactPath != "" {
		r.pauseDeploymentRun(
			ctx,
			runCtx,
			cancel,
			run,
			stepIndex,
			step,
			logWriter,
		)
		return false
	}
	return true
}

func (r *DeploymentRunner) runRemoteDeploymentStep(
	ctx, runCtx context.Context,
	run *deploymentRun,
	stepIndex int,
	step deploymentStep,
	logWriter *broadcastWriter,
) bool {
	deploymentID := run.deploymentID
	stageKey := db.GetStagingSnapshotParams{
		DeploymentID: deploymentID,
		StepIndex:    int64(stepIndex),
	}
	stageErr := r.prepareRemoteStaging(
		runCtx,
		remoteStagingInput{
			key:     stageKey,
			volume:  run.handoff,
			current: run.transferred,
		},
	)
	// Shutdown drains local resources without waiting for agent ACKs.
	if run.artifactWork {
		r.localWork.Done()
		run.artifactWork = false
	}
	err := stageErr
	stagingFailed := stageErr != nil
	if err == nil {
		err = r.runRemoteStep(ctx, runCtx, remoteStepRequest{
			deploymentID: deploymentID,
			stepIndex:    int64(stepIndex),
			step:         step,
			logWriter:    logWriter,
		})
	}
	if err == nil {
		var next *os.File
		next, err = r.mergeRemoteStaging(runCtx, stageKey)
		err = errors.Join(err, r.replaceTransfer(deploymentID, next))
		run.transferred = next
		stagingFailed = err != nil
	}
	if err != nil {
		if stagingFailed {
			if _, logErr := fmt.Fprintf(logWriter,
				"step %q: %v\n", step.Name, err); logErr != nil {
				slog.Error("persist staging failure", "err", logErr)
			}
			logWriter.Flush()
			logWriter.finishState(err, runCtx.Err() != nil)
		}
		if errors.Is(err, errContainerCleanup) {
			r.persistCompletion(ctx, runCtx, deploymentID,
				"cleanup_unconfirmed", false)
			return false
		}
		if errors.Is(err, errDeploymentCancelled) ||
			(stagingFailed && runCtx.Err() != nil) {
			r.finalizeCancellation(ctx, deploymentID)
			return false
		}
		r.failStep(ctx, runCtx, events.Event{
			Type: events.DeploymentFailed, DeploymentID: deploymentID,
			ProjectID: run.projectID, EnvironmentID: run.environmentID,
			Message: run.scrubber.Scrub(fmt.Sprintf(
				"Deployment #%d failed on %s: %v",
				deploymentID, run.envName, err,
			)),
		}, false)
		return false
	}
	logWriter.finishState(nil, false)
	return true
}

func (r *DeploymentRunner) runLocalDeploymentStep(
	ctx, runCtx context.Context,
	run *deploymentRun,
	step deploymentStep,
	logWriter *broadcastWriter,
) bool {
	deploymentID := run.deploymentID
	projectID, environmentID := run.projectID, run.environmentID
	recorded, err := r.recordContainerNamespace(ctx, deploymentID)
	if err != nil {
		r.failStep(ctx, runCtx, events.Event{
			Type:          events.DeploymentFailed,
			DeploymentID:  deploymentID,
			ProjectID:     projectID,
			EnvironmentID: environmentID,
			Message: fmt.Sprintf(
				"Deployment #%d failed before container execution: %v",
				deploymentID,
				err,
			),
		}, true)
		return false
	}
	if recorded != 1 {
		deployment, loadErr := r.repo.Queries.GetDeployment(
			ctx, deploymentID,
		)
		if loadErr == nil && deployment.ContainerNamespace.Valid {
			r.persistCompletion(ctx, runCtx, deploymentID,
				"cleanup_unconfirmed", false)
			return false
		}
		if loadErr == nil && deployment.Status == "cancelled" {
			return false
		}
		r.failStep(ctx, runCtx, events.Event{
			Type:          events.DeploymentFailed,
			DeploymentID:  deploymentID,
			ProjectID:     projectID,
			EnvironmentID: environmentID,
			Message: fmt.Sprintf(
				"Deployment #%d failed before container execution: container namespace was not recorded",
				deploymentID,
			),
		}, true)
		return false
	}
	if run.handoff.volume == "" {
		run.handoff, err = r.stageDeployment(
			runCtx,
			deploymentID,
			run.environment,
		)
		if err != nil {
			writeErr := logWriter.failStaging(err, runCtx.Err() != nil)
			r.failStep(ctx, runCtx, events.Event{
				Type:          events.DeploymentFailed,
				DeploymentID:  deploymentID,
				ProjectID:     projectID,
				EnvironmentID: environmentID,
				Message: run.scrubber.Scrub("Deployment staging failed: " +
					errors.Join(err, writeErr).Error()),
			}, true)
			return false
		}
	}
	maxAttempts := int(step.MaxRetries + 1)
	if run.transferred != nil {
		if err := r.importRemoteStaging(
			runCtx,
			run.handoff,
			run.transferred,
		); err != nil {
			_ = logWriter.failStaging(err, runCtx.Err() != nil)
			r.failUnlessCancelled(ctx, runCtx, deploymentID)
			return false
		}
		if err := r.replaceTransfer(deploymentID, nil); err != nil {
			r.failUnlessCancelled(ctx, runCtx, deploymentID)
			return false
		}
		run.transferred = nil
	}
	lastErr, completed := r.runLocalDeploymentAttempts(
		ctx, runCtx, run, step, logWriter, maxAttempts,
	)
	if !completed {
		return false
	}
	if lastErr != nil {
		if errors.Is(lastErr, errContainerCleanup) {
			r.persistCompletion(ctx, runCtx, deploymentID,
				"cleanup_unconfirmed", false)
			return false
		}
		r.failStep(ctx, runCtx, events.Event{
			Type: events.DeploymentFailed, DeploymentID: deploymentID,
			ProjectID: projectID, EnvironmentID: environmentID,
			Message: fmt.Sprintf(
				"Deployment #%d failed on %s: %v",
				deploymentID, run.envName, lastErr,
			),
		}, true)
		return false
	}
	return true
}

func (r *DeploymentRunner) runLocalDeploymentAttempts(
	ctx, runCtx context.Context,
	run *deploymentRun,
	step deploymentStep,
	logWriter *broadcastWriter,
	maxAttempts int,
) (error, bool) {
	deploymentID := run.deploymentID
	var lastErr error
	for index := range maxAttempts {
		attempt := index + 1
		lastErr = r.runStepAttempt(runCtx, localStepAttempt{
			deploymentID: deploymentID,
			step:         step,
			logWriter:    logWriter,
			environment:  run.environment,
			attempt:      attempt,
			artifact:     run.artifact,
			handoff:      run.handoff,
			approved:     run.approved,
		})
		if lastErr == nil {
			break
		}
		if errors.Is(lastErr, errContainerCleanup) {
			break
		}
		if runCtx.Err() != nil {
			r.finalizeCancellation(ctx, deploymentID)
			return lastErr, false
		}

		dep, _ := r.repo.Queries.GetDeployment(ctx, deploymentID)
		if dep.Status == "cancelled" {
			return lastErr, false
		}

		if attempt < maxAttempts {
			if _, err := fmt.Fprintf(logWriter,
				"step %q: retrying (attempt %d of %d)\n",
				step.Name,
				attempt+1,
				maxAttempts); err != nil {
				slog.Error(
					"persist retry message",
					"deployment_id",
					deploymentID,
					"err",
					err,
				)
			}
			logWriter.Flush()
		}
	}
	return lastErr, true
}

func (r *DeploymentRunner) pauseDeploymentRun(
	ctx, runCtx context.Context,
	cancel context.CancelFunc,
	run *deploymentRun,
	stepIndex int,
	step deploymentStep,
	logWriter *broadcastWriter,
) {
	deploymentID := run.deploymentID
	err := r.captureArtifactGate(
		runCtx,
		deploymentID,
		int64(stepIndex),
		step,
		run.handoff,
	)
	if err == nil {
		err = r.cleanupArtifact(deploymentID)
		run.cleanupDone = err == nil
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
		if _, logErr := fmt.Fprintf(logWriter,
			"Artifact approval failed: %v\n", err); logErr != nil {
			slog.Error(
				"persist artifact approval failure", "err", logErr,
			)
		}
		logWriter.Flush()
		logWriter.finishState(err, runCtx.Err() != nil)
		r.failUnlessCancelled(ctx, runCtx, deploymentID)
		return
	}
	cancel()
	r.publish(
		ctx,
		events.Event{
			Type:          events.ArtifactAwaitingApproval,
			DeploymentID:  deploymentID,
			ProjectID:     run.projectID,
			EnvironmentID: run.environmentID,
			Message:       "Deployment artifact is ready for approval",
		},
	)
}

func (r *DeploymentRunner) finishDeploymentRun(
	ctx, runCtx context.Context,
	run *deploymentRun,
) {
	deploymentID := run.deploymentID
	projectID, environmentID := run.projectID, run.environmentID
	envName := run.envName
	if runCtx.Err() != nil {
		r.finalizeCancellation(ctx, deploymentID)
		return
	}
	if err := r.verifyDeployment(ctx, runCtx, deploymentID,
		run.environment, run.secretValues, run.artifact); err != nil {
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
			ProjectID:     projectID,
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
		ProjectID:     projectID,
		EnvironmentID: environmentID,
		Message: fmt.Sprintf(
			"Deployment #%d succeeded on %s",
			deploymentID,
			envName,
		),
	})
}

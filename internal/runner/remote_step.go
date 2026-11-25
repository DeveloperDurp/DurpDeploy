package runner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"durpdeploy/internal/agentexecution"
	"durpdeploy/internal/db"
)

var errDeploymentCancelled = errors.New("deployment cancelled")

type remoteStepRequest struct {
	deploymentID int64
	stepIndex    int64
	step         deploymentStep
	logWriter    *broadcastWriter
}

func (r *DeploymentRunner) runRemoteStep(
	ctx, cancelCtx context.Context,
	request remoteStepRequest,
) (result error) {
	if request.step.Kind == "ansible" {
		return errors.New(
			"native Ansible execution is unsupported on remote agents",
		)
	}
	if err := agentexecution.ValidateLimitsWithPolicy(request.step.TimeoutSeconds, request.step.MaxRetries, r.repo.ResourceLimits().Execution); err != nil {
		return err
	}
	if cancelCtx.Err() != nil {
		return errDeploymentCancelled
	}
	if err := request.logWriter.state("waiting"); err != nil {
		return err
	}
	defer func() {
		if result != nil {
			request.logWriter.finishState(
				result,
				errors.Is(result, errDeploymentCancelled),
			)
		}
	}()
	if err := r.queueRemoteStep(ctx, request); err != nil {
		return err
	}
	return r.waitForRemoteStep(ctx, cancelCtx, request)
}

func (r *DeploymentRunner) queueRemoteStep(
	ctx context.Context,
	request remoteStepRequest,
) error {
	created, err := r.repo.QueueRemoteStepRuns(
		ctx, request.deploymentID, request.stepIndex,
	)
	if err != nil {
		if errors.Is(err, agentexecution.ErrLimits) {
			_, logErr := fmt.Fprintf(
				request.logWriter,
				"step %q: %v\n",
				request.step.Name,
				err,
			)
			request.logWriter.Flush()
			return errors.Join(err, logErr)
		}
		return err
	}
	if created == 0 {
		return r.noCompatibleRemoteAgents(ctx, request)
	}
	_, _ = fmt.Fprintf(request.logWriter,
		"step %q: queued for %d matching agent(s)\n",
		request.step.Name,
		created)
	request.logWriter.Flush()
	return nil
}

func (r *DeploymentRunner) noCompatibleRemoteAgents(
	ctx context.Context,
	request remoteStepRequest,
) error {
	capabilities := "staging/1"
	if _, err := r.repo.Queries.GetDeploymentArtifact(
		ctx,
		request.deploymentID,
	); err == nil {
		capabilities += " and artifact/1"
	} else if !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		return err
	}
	selectedInterpreter := request.step.Interpreter
	if selectedInterpreter == "" {
		selectedInterpreter = "bash"
	}
	message := fmt.Sprintf(
		"step %q: no compatible agents support interpreter %q and match the environment, labels, and %s capabilities; upgrade participating agents",
		request.step.Name,
		selectedInterpreter,
		capabilities,
	)
	_, _ = request.logWriter.Write([]byte(message + "\n"))
	request.logWriter.Flush()
	return errors.New(message)
}

func (r *DeploymentRunner) waitForRemoteStep(
	ctx, cancelCtx context.Context,
	request remoteStepRequest,
) error {
	timeout := time.Duration(
		r.repo.ResourceLimits().Execution.DefaultTimeoutSeconds,
	) * time.Second
	if request.step.TimeoutSeconds > 0 {
		timeout = time.Duration(request.step.TimeoutSeconds) * time.Second
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	progress := remoteStepProgress{
		timeout:     timeout,
		remaining:   make(map[string]time.Duration),
		transferred: make(map[string]time.Duration),
	}
	defer r.repo.ClearStagingTransfers(db.ListRemoteStepRunsForRunnerParams{
		DeploymentID: request.deploymentID, StepIndex: request.stepIndex,
	})
	progress.lastChecked = time.Now()
	timedOut := false
	operatorCancelled := false
	cancellationNeeded := false
	cancellationRequested := false
	cancelSignal := cancelCtx.Done()
	for {
		if cancellationNeeded && !cancellationRequested {
			_, err := r.repo.Queries.RequestRemoteStepCancellation(
				ctx,
				db.RequestRemoteStepCancellationParams{
					Now: sql.NullInt64{
						Int64: time.Now().Unix(),
						Valid: true,
					},
					DeploymentID: request.deploymentID,
				},
			)
			if err != nil {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-ticker.C:
				}
				continue
			}
			cancellationRequested = true
		}
		runs, err := r.repo.Queries.ListRemoteStepRunsForRunner(
			ctx,
			db.ListRemoteStepRunsForRunnerParams{
				DeploymentID: request.deploymentID,
				StepIndex:    request.stepIndex,
			},
		)
		if err != nil {
			return err
		}
		observation, err := r.observeRemoteStepRuns(
			request, runs, &progress, operatorCancelled,
		)
		if err != nil {
			return err
		}
		if observation.expired {
			timedOut = timedOut || progress.failureAgent == ""
			cancellationNeeded = true
		}
		if observation.hasFailure && observation.hasActive {
			cancellationNeeded = true
		}
		if done, err := observation.terminalResult(
			request.step.Name, &progress, timedOut, operatorCancelled,
		); done {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-cancelSignal:
			operatorCancelled = true
			cancellationNeeded = true
			cancelSignal = nil
		case <-ticker.C:
		}
	}
}

type remoteStepProgress struct {
	timeout      time.Duration
	remaining    map[string]time.Duration
	transferred  map[string]time.Duration
	lastChecked  time.Time
	failureAgent string
	started      bool
}

type remoteStepObservation struct {
	allSucceeded       bool
	hasActive          bool
	hasFailure         bool
	cleanupUnconfirmed bool
	expired            bool
}

func (r *DeploymentRunner) observeRemoteStepRuns(
	request remoteStepRequest,
	runs []db.ListRemoteStepRunsForRunnerRow,
	progress *remoteStepProgress,
	operatorCancelled bool,
) (remoteStepObservation, error) {
	observation := remoteStepObservation{allSucceeded: len(runs) > 0}
	now := time.Now()
	elapsed := now.Sub(progress.lastChecked)
	progress.lastChecked = now
	for _, run := range runs {
		if !progress.started && run.StartedAt.Valid {
			if err := request.logWriter.state("running"); err != nil {
				return remoteStepObservation{}, err
			}
			progress.started = true
		}
		switch run.State {
		case "succeeded":
		case "cancelled":
			observation.allSucceeded = false
			observation.hasFailure = observation.hasFailure ||
				!operatorCancelled
		case "failed", "lost", "cancel_unconfirmed", "cleanup_unconfirmed":
			observation.cleanupUnconfirmed = observation.cleanupUnconfirmed ||
				run.State == "cleanup_unconfirmed"
			observation.allSucceeded = false
			observation.hasFailure = true
			if progress.failureAgent == "" {
				progress.failureAgent = run.AgentID
			}
		default:
			observation.allSucceeded = false
			observation.hasActive = true
			if r.consumeRemoteAgentBudget(
				request,
				run,
				progress,
				now,
				elapsed,
			) {
				observation.expired = true
			}
		}
	}
	return observation, nil
}

func (r *DeploymentRunner) consumeRemoteAgentBudget(
	request remoteStepRequest,
	run db.ListRemoteStepRunsForRunnerRow,
	progress *remoteStepProgress,
	now time.Time,
	elapsed time.Duration,
) bool {
	budget, exists := progress.remaining[run.AgentID]
	if !exists {
		budget = progress.timeout
	}
	// Maintenance pauses waiting targets, not issued execution.
	if run.State != "waiting" || run.Draining == 0 {
		transferTime := r.repo.StagingTransferDuration(
			db.GetStagingSnapshotParams{
				DeploymentID: request.deploymentID,
				StepIndex:    request.stepIndex,
				Producer:     run.AgentID,
			},
			now,
		)
		budget -= max(
			0,
			elapsed-(transferTime-progress.transferred[run.AgentID]),
		)
		progress.transferred[run.AgentID] = transferTime
	}
	progress.remaining[run.AgentID] = budget
	return budget <= 0
}

func (observation remoteStepObservation) terminalResult(
	stepName string,
	progress *remoteStepProgress,
	timedOut, operatorCancelled bool,
) (bool, error) {
	if observation.cleanupUnconfirmed && !observation.hasActive {
		return true, errContainerCleanup
	}
	if operatorCancelled && !observation.hasActive {
		if observation.hasFailure {
			return true, fmt.Errorf(
				"step %q cancellation was not confirmed by agent %s",
				stepName,
				progress.failureAgent,
			)
		}
		return true, errDeploymentCancelled
	}
	if observation.hasFailure && !observation.hasActive {
		if timedOut {
			return true, fmt.Errorf(
				"step %q timed out after %s",
				stepName,
				progress.timeout,
			)
		}
		return true, fmt.Errorf(
			"step %q failed on agent %s",
			stepName,
			progress.failureAgent,
		)
	}
	return observation.allSucceeded, nil
}

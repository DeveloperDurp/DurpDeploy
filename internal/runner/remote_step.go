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
	ctx context.Context,
	cancelCtx context.Context,
	request remoteStepRequest,
) (result error) {
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
	_, _ = fmt.Fprintf(request.logWriter,
		"step %q: queued for %d matching agent(s)\n",
		request.step.Name,
		created)
	request.logWriter.Flush()

	timeout := time.Duration(
		r.repo.ResourceLimits().Execution.DefaultTimeoutSeconds,
	) * time.Second
	if request.step.TimeoutSeconds > 0 {
		timeout = time.Duration(request.step.TimeoutSeconds) * time.Second
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	remaining := make(map[string]time.Duration)
	transferred := make(map[string]time.Duration)
	defer r.repo.ClearStagingTransfers(db.ListRemoteStepRunsForRunnerParams{
		DeploymentID: request.deploymentID, StepIndex: request.stepIndex,
	})
	lastChecked := time.Now()
	timedOut := false
	operatorCancelled := false
	cancellationNeeded := false
	cancellationRequested := false
	failureAgent := ""
	cancelSignal := cancelCtx.Done()
	started := false
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
		allSucceeded := len(runs) > 0
		hasActive := false
		hasFailure := false
		cleanupUnconfirmed := false
		expired := false
		now := time.Now()
		elapsed := now.Sub(lastChecked)
		lastChecked = now
		for _, run := range runs {
			if !started && run.StartedAt.Valid {
				if err := request.logWriter.state("running"); err != nil {
					return err
				}
				started = true
			}
			switch run.State {
			case "succeeded":
			case "cancelled":
				allSucceeded = false
				if !operatorCancelled {
					hasFailure = true
				}
			case "failed", "lost", "cancel_unconfirmed", "cleanup_unconfirmed":
				cleanupUnconfirmed = cleanupUnconfirmed ||
					run.State == "cleanup_unconfirmed"
				allSucceeded = false
				hasFailure = true
				if failureAgent == "" {
					failureAgent = run.AgentID
				}
			default:
				allSucceeded = false
				hasActive = true
				budget, exists := remaining[run.AgentID]
				if !exists {
					budget = timeout
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
						elapsed-(transferTime-transferred[run.AgentID]),
					)
					transferred[run.AgentID] = transferTime
				}
				remaining[run.AgentID] = budget
				if budget <= 0 {
					expired = true
				}
			}
		}
		if expired {
			if failureAgent == "" {
				timedOut = true
			}
			cancellationNeeded = true
		}
		if cleanupUnconfirmed && !hasActive {
			return errContainerCleanup
		}
		if operatorCancelled && !hasActive {
			if hasFailure {
				return fmt.Errorf(
					"step %q cancellation was not confirmed by agent %s",
					request.step.Name,
					failureAgent,
				)
			}
			return errDeploymentCancelled
		}
		if hasFailure && hasActive {
			cancellationNeeded = true
		} else if hasFailure {
			if timedOut {
				return fmt.Errorf(
					"step %q timed out after %s",
					request.step.Name,
					timeout,
				)
			}
			return fmt.Errorf(
				"step %q failed on agent %s",
				request.step.Name,
				failureAgent,
			)
		}
		if allSucceeded {
			return nil
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

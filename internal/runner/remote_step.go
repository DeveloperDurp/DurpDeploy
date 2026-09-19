package runner

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

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
) error {
	if cancelCtx.Err() != nil {
		return errDeploymentCancelled
	}
	created, err := r.repo.QueueRemoteStepRuns(
		ctx, request.deploymentID, request.stepIndex,
	)
	if err != nil {
		return err
	}
	if created == 0 {
		return fmt.Errorf(
			"step %q: no active paired agents match the environment and label",
			request.step.Name,
		)
	}
	_, _ = request.logWriter.Write([]byte(fmt.Sprintf(
		"step %q: queued for %d matching agent(s)\n",
		request.step.Name,
		created,
	)))
	request.logWriter.Flush()

	timeout := defaultStepTimeout
	if request.step.TimeoutSeconds > 0 {
		timeout = time.Duration(request.step.TimeoutSeconds) * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	timedOut := false
	operatorCancelled := false
	cancellationNeeded := false
	cancellationRequested := false
	failureAgent := ""
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
		runs, err := r.repo.Queries.ListRemoteStepRuns(
			ctx,
			db.ListRemoteStepRunsParams{
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
		for _, run := range runs {
			switch run.State {
			case "succeeded":
			case "cancelled":
				allSucceeded = false
				if !operatorCancelled {
					hasFailure = true
				}
			case "failed", "lost", "cancel_unconfirmed":
				allSucceeded = false
				hasFailure = true
				if failureAgent == "" {
					failureAgent = run.AgentID
				}
			default:
				allSucceeded = false
				hasActive = true
			}
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
		case <-timer.C:
			if failureAgent == "" {
				timedOut = true
			}
			cancellationNeeded = true
		case <-ticker.C:
		}
	}
}

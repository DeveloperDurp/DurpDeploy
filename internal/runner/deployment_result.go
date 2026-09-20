package runner

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func (r *DeploymentRunner) failStep(
	ctx context.Context,
	cancelCtx context.Context,
	event events.Event,
	cancellationWins bool,
) {
	status, persisted := r.persistCompletion(
		ctx, cancelCtx, event.DeploymentID, "failed", cancellationWins,
	)
	if persisted && status == "failed" {
		r.publish(ctx, event)
	}
}

func (r *DeploymentRunner) completeDeployment(
	ctx context.Context,
	cancelCtx context.Context,
	deploymentID int64,
	status string,
	cancellationWins bool,
) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancellationWins && cancelCtx.Err() != nil {
		status = "cancelled"
	}
	var err error
	if status == "cancelled" {
		_, err = r.repo.Queries.CancelStepDeployment(ctx, deploymentID)
	} else {
		err = r.repo.Queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID: deploymentID, Status: status,
				FinishedAt: sql.NullInt64{
					Int64: time.Now().Unix(), Valid: true,
				},
			},
		)
	}
	if err == nil {
		delete(r.cancels, deploymentID)
	}
	return status, err
}

func (r *DeploymentRunner) finalizeCancellation(
	ctx context.Context,
	deploymentID int64,
) {
	r.persistCompletion(
		ctx, context.Background(), deploymentID, "cancelled", false,
	)
}

func (r *DeploymentRunner) persistCompletion(
	ctx context.Context,
	cancelCtx context.Context,
	deploymentID int64,
	status string,
	cancellationWins bool,
) (string, bool) {
	finalStatus, err := r.completeDeployment(
		ctx, cancelCtx, deploymentID, status, cancellationWins,
	)
	if err == nil {
		return finalStatus, true
	}
	slog.Error(
		"persist deployment terminal status",
		"deployment_id", deploymentID,
		"status", finalStatus,
		"err", err,
	)
	r.UnregisterCancel(deploymentID)
	retryCtx, cancelRetry := context.WithTimeout(
		context.WithoutCancel(ctx), 30*time.Second,
	)
	go r.retryCompletion(
		retryCtx,
		cancelRetry,
		cancelCtx,
		deploymentID,
		status,
		cancellationWins,
	)
	return finalStatus, false
}

func (r *DeploymentRunner) retryCompletion(
	ctx context.Context,
	cancel context.CancelFunc,
	cancelCtx context.Context,
	deploymentID int64,
	status string,
	cancellationWins bool,
) {
	defer cancel()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			slog.Error(
				"deployment terminal status retry exhausted",
				"deployment_id", deploymentID,
				"status", status,
				"err", ctx.Err(),
			)
			return
		case <-ticker.C:
			finalStatus, err := r.completeDeployment(
				ctx, cancelCtx, deploymentID, status, cancellationWins,
			)
			if err == nil {
				slog.Info(
					"recovered deployment terminal status",
					"deployment_id", deploymentID,
					"status", finalStatus,
				)
				return
			}
		}
	}
}

// publish is a no-op when the runner has no event bus wired (SetEventBus
// never called), so tests and the recovery path don't need a bus.
func (r *DeploymentRunner) publish(
	ctx context.Context,
	event events.Event,
) {
	if r.bus == nil {
		return
	}
	r.bus.Publish(ctx, event)
}

func (r *DeploymentRunner) failUnlessCancelled(
	ctx context.Context,
	cancelCtx context.Context,
	deploymentID int64,
) {
	if cancelCtx.Err() != nil {
		r.finalizeCancellation(ctx, deploymentID)
		return
	}
	deployment, err := r.repo.Queries.GetDeployment(ctx, deploymentID)
	if err != nil {
		slog.Error(
			"load deployment after runner failure",
			"deployment_id", deploymentID,
			"err", err,
		)
		return
	}
	if deployment.Status == "cancelled" {
		return
	}
	r.persistCompletion(
		ctx, cancelCtx, deploymentID, "failed", true,
	)
}

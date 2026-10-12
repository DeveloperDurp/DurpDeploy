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
	localCleanupConfirmed bool,
) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cancellationWins && cancelCtx.Err() != nil {
		status = "cancelled"
	}
	err := r.repo.WithDeploymentTx(
		ctx,
		deploymentID,
		func(ctx context.Context, q *db.Queries) error {
			deployment, err := q.GetDeployment(ctx, deploymentID)
			if err != nil {
				return err
			}
			if localCleanupConfirmed && status == "cleanup_unconfirmed" {
				if err := q.ConfirmRemoteDeploymentLocalCleanup(
					ctx,
					deploymentID,
				); err != nil {
					return err
				}
			}
			switch deployment.Status {
			case "succeeded",
				"failed",
				"cancelled",
				"rejected",
				"expired",
				"cleanup_unconfirmed":
				if status != "cleanup_unconfirmed" {
					status = deployment.Status
					return nil
				}
			}
			if status == "cancelled" {
				_, err = q.CancelStepDeployment(ctx, deploymentID)
			} else {
				err = q.UpdateDeploymentStatus(
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
				err = q.FinishArtifactGates(ctx, db.FinishArtifactGatesParams{
					DeploymentID: deploymentID, Status: "cancelled",
				})
			}
			if err == nil {
				verificationStatus := "failed"
				if status == "cancelled" {
					verificationStatus = "cancelled"
				}
				err = q.FinishDeploymentVerification(ctx,
					db.FinishDeploymentVerificationParams{
						DeploymentID: deploymentID, Status: verificationStatus,
					})
			}
			return err
		},
	)
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
	localCleanupConfirmed := r.cleanupArtifact(deploymentID) == nil
	if !localCleanupConfirmed {
		status = "cleanup_unconfirmed"
		cancellationWins = false
	}
	finalStatus, err := r.completeDeployment(
		ctx,
		cancelCtx,
		deploymentID,
		status,
		cancellationWins,
		localCleanupConfirmed,
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
		localCleanupConfirmed,
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
	localCleanupConfirmed bool,
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
				ctx,
				cancelCtx,
				deploymentID,
				status,
				cancellationWins,
				localCleanupConfirmed,
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
	release, err := r.repo.Queries.GetRelease(ctx, deployment.ReleaseID)
	if err != nil {
		slog.Error(
			"load release after runner failure",
			"deployment_id",
			deploymentID,
			"err",
			err,
		)
		r.persistCompletion(ctx, cancelCtx, deploymentID, "failed", true)
		return
	}
	r.failStep(ctx, cancelCtx, events.Event{
		Type: events.DeploymentFailed, DeploymentID: deploymentID,
		ProjectID: release.ProjectID, EnvironmentID: deployment.EnvironmentID,
		Message: "Deployment failed",
	}, true)
}

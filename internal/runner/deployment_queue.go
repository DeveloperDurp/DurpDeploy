package runner

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"durpdeploy/internal/repository"
)

// ServeQueue repairs missed launches without relying on an in-memory wakeup.
func (r *DeploymentRunner) ServeQueue(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := r.repo.ReconcileDeploymentQueues(ctx); err != nil {
				slog.Error("reconcile deployment queues", "err", err)
				continue
			}
			pending, err := r.repo.Queries.ListPendingDeployments(ctx)
			if err != nil {
				slog.Error("list admitted deployments", "err", err)
				continue
			}
			for _, d := range pending {
				if !d.AssignedAgentID.Valid {
					go r.Run(ctx, d.ID, d.ReleaseID, d.EnvironmentID)
				}
			}
		}
	}
}

// CancelPrestart also handles work admitted between the handler read and lock.
func (r *DeploymentRunner) CancelPrestart(
	ctx context.Context, id int64,
) (string, error) {
	err := r.repo.CancelQueuedDeployment(ctx, id)
	if errors.Is(err, repository.ErrDeploymentQueueConflict) {
		return "cancellation_requested", r.Cancel(id)
	}
	return "cancelled", err
}

package runner

import (
	"context"
	"log/slog"
	"time"
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

package main

import (
	"context"
	"log/slog"
	"time"

	"durpdeploy/internal/repository"
)

func maintainArtifactGates(ctx context.Context, repo *repository.Repository) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := repo.MaintainArtifactGates(ctx); err != nil {
				slog.Error("artifact gate maintenance", "err", err)
			}
		}
	}
}

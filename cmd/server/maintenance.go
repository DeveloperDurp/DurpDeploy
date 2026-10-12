package main

import (
	"context"
	"log/slog"
	"time"

	"durpdeploy/internal/mfa"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
)

func maintainServerState(ctx context.Context, repo *repository.Repository,
	rnr *runner.DeploymentRunner) {
	challenges := mfa.NewChallengeService(mfa.ChallengeServiceConfig{
		Repository: repo,
	})
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := challenges.CleanupExpired(ctx); err != nil {
				slog.Error("MFA challenge cleanup", "err", err)
			}
			if err := repo.MaintainArtifactGates(ctx); err != nil {
				slog.Error("artifact gate maintenance", "err", err)
			}
			if err := rnr.CleanupArtifactGateImages(ctx); err != nil {
				slog.Error("artifact image cleanup", "err", err)
			}
		}
	}
}

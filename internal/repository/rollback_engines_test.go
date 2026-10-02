package repository

import (
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestVerificationRollbackAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		firstRepo, secondRepo := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		ctx := t.Context()
		if _, err := firstRepo.Queries.UpdateEnvironment(
			ctx,
			db.UpdateEnvironmentParams{
				ID:                         1,
				Name:                       "production",
				ConfigureVerification:      1,
				VerificationType:           "http",
				VerificationTarget:         "https://example.com/health",
				VerificationTimeoutSeconds: 5,
			},
		); err != nil {
			t.Fatal(err)
		}
		good, err := firstRepo.CreateDeployment(ctx, db.CreateDeploymentParams{
			ReleaseID: 1, EnvironmentID: 1, Status: "pending",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := firstRepo.Queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:     good.Deployment.ID,
				Status: "succeeded",
			},
		); err != nil {
			t.Fatal(err)
		}
		otherEnv, err := firstRepo.Queries.CreateEnvironment(
			ctx,
			db.CreateEnvironmentParams{Name: "other"},
		)
		if err != nil {
			t.Fatal(err)
		}
		v2, err := firstRepo.Queries.CreateRelease(ctx, db.CreateReleaseParams{
			ProjectID: 1, Version: "v2", StepsJson: "[]",
		})
		if err != nil {
			t.Fatal(err)
		}
		// A newer successful deployment elsewhere must not become the target.
		elsewhere, err := firstRepo.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: otherEnv.ID, Status: "pending",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := firstRepo.Queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:     elsewhere.Deployment.ID,
				Status: "succeeded",
			},
		); err != nil {
			t.Fatal(err)
		}
		failed, err := firstRepo.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID: v2.ID, EnvironmentID: 1, Status: "pending",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		if err := firstRepo.Queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{
				ID:     failed.Deployment.ID,
				Status: "failed",
			},
		); err != nil {
			t.Fatal(err)
		}
		preview, err := firstRepo.PreviewRollback(ctx, failed.Deployment.ID)
		if err != nil || preview.TargetDeploymentID != good.Deployment.ID {
			t.Fatalf("preview=%+v err=%v", preview, err)
		}
		// Two connections confirm the same preview concurrently; only one wins.
		start := make(chan struct{})
		results := make(chan error, 2)
		for _, repo := range []*Repository{firstRepo, secondRepo} {
			go func() {
				<-start
				_, err := repo.CreateRollback(
					ctx,
					failed.Deployment.ID,
					preview.TargetDeploymentID,
				)
				results <- err
			}()
		}
		close(start)
		successes, conflicts := 0, 0
		for range 2 {
			err := <-results
			if err == nil {
				successes++
			} else if errors.Is(err, ErrRollbackConflict) {
				conflicts++
			} else {
				t.Fatalf("rollback: %v", err)
			}
		}
		if successes != 1 || conflicts != 1 {
			t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
		}
		latest, err := firstRepo.Queries.GetLatestProjectEnvironmentDeployment(
			ctx,
			db.GetLatestProjectEnvironmentDeploymentParams{
				ProjectID: 1, EnvironmentID: 1,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		provenance, err := firstRepo.Queries.GetDeploymentRollback(
			ctx,
			latest.ID,
		)
		if err != nil || provenance.TargetDeploymentID != good.Deployment.ID ||
			provenance.SourceVersion != "v2" {
			t.Fatalf("provenance=%+v err=%v", provenance, err)
		}
		check, err := firstRepo.Queries.GetDeploymentVerification(
			ctx,
			latest.ID,
		)
		if err != nil || check.Type != "http" || check.TimeoutSeconds != 5 {
			t.Fatalf("verification=%+v err=%v", check, err)
		}
		if _, err := firstRepo.Queries.StartDeploymentVerification(
			ctx,
			latest.ID,
		); err != nil {
			t.Fatal(err)
		}
		if err := firstRepo.Queries.UpdateDeploymentStatus(
			ctx,
			db.UpdateDeploymentStatusParams{ID: latest.ID, Status: "failed"},
		); err != nil {
			t.Fatal(err)
		}
		if err := firstRepo.Queries.ReconcileTerminalVerifications(
			ctx,
		); err != nil {
			t.Fatal(err)
		}
		check, err = firstRepo.Queries.GetDeploymentVerification(ctx, latest.ID)
		if err != nil || check.Status != "failed" || !check.FinishedAt.Valid {
			t.Fatalf("recovered verification=%+v err=%v", check, err)
		}
		locked, err := firstRepo.Queries.LockUnusedReleaseSnapshot(ctx, 1)
		if err != nil || locked != 0 {
			t.Fatalf("used release lock=%d err=%v", locked, err)
		}
	})
}

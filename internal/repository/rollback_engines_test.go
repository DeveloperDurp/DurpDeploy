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
		good, failed := seedRollbackEngine(t, firstRepo)
		preview, err := firstRepo.PreviewRollback(t.Context(), failed)
		if err != nil || preview.TargetDeploymentID != good {
			t.Fatalf("preview=%+v err=%v", preview, err)
		}
		confirmRollbackConcurrently(t, firstRepo, secondRepo, failed, good)
		latest := assertRollbackEngineSnapshot(t, firstRepo, good)
		assertRollbackVerificationRecovery(t, firstRepo, latest)
	})
}

func seedRollbackEngine(t *testing.T, firstRepo *Repository) (int64, int64) {
	t.Helper()
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
	return good.Deployment.ID, failed.Deployment.ID
}

func confirmRollbackConcurrently(
	t *testing.T, firstRepo, secondRepo *Repository, sourceID, targetID int64,
) {
	t.Helper()
	ctx := t.Context()
	// Two connections confirm the same preview concurrently; only one wins.
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, repo := range []*Repository{firstRepo, secondRepo} {
		go func() {
			<-start
			_, err := repo.CreateRollback(
				ctx,
				sourceID,
				targetID,
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
}

func assertRollbackEngineSnapshot(
	t *testing.T, firstRepo *Repository, targetID int64,
) int64 {
	t.Helper()
	ctx := t.Context()
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
	if err != nil || provenance.TargetDeploymentID != targetID ||
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
	return latest.ID
}

func assertRollbackVerificationRecovery(
	t *testing.T, firstRepo *Repository, deploymentID int64,
) {
	t.Helper()
	ctx := t.Context()
	if _, err := firstRepo.Queries.StartDeploymentVerification(
		ctx,
		deploymentID,
	); err != nil {
		t.Fatal(err)
	}
	if err := firstRepo.Queries.UpdateDeploymentStatus(
		ctx,
		db.UpdateDeploymentStatusParams{ID: deploymentID, Status: "failed"},
	); err != nil {
		t.Fatal(err)
	}
	if err := firstRepo.Queries.ReconcileTerminalVerifications(
		ctx,
	); err != nil {
		t.Fatal(err)
	}
	check, err := firstRepo.Queries.GetDeploymentVerification(ctx, deploymentID)
	if err != nil || check.Status != "failed" || !check.FinishedAt.Valid {
		t.Fatalf("recovered verification=%+v err=%v", check, err)
	}
	locked, err := firstRepo.Queries.LockUnusedReleaseSnapshot(ctx, 1)
	if err != nil || locked != 0 {
		t.Fatalf("used release lock=%d err=%v", locked, err)
	}
}

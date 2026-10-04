package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/secret"
)

func TestRollbackWaitsForCompetingReleaseCreationAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		creator, rollback := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		good, failed := seedRollbackEngine(t, creator)
		release, err := creator.Queries.CreateRelease(t.Context(),
			db.CreateReleaseParams{
				ProjectID: 1, Version: "competing-v3", StepsJson: "[]",
			})
		if err != nil {
			t.Fatal(err)
		}
		created, commit, done := holdCompetingDeployment(t, creator, release.ID)
		defer commit()
		select {
		case <-created:
		case err := <-done:
			t.Fatalf("create competing deployment: %v", err)
		}
		// A different release still holds the shared project lock. The rollback
		// must wait before checking whether its source remains current.
		ctx, cancel := context.WithTimeout(t.Context(), 500*time.Millisecond)
		defer cancel()
		_, err = rollback.CreateRollback(ctx, failed, good)
		if err == nil || ctx.Err() != context.DeadlineExceeded {
			t.Fatalf("rollback escaped the competing project lock: %v", err)
		}
		commit()
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		if _, err := rollback.CreateRollback(
			t.Context(),
			failed,
			good,
		); !errors.Is(
			err,
			ErrRollbackConflict,
		) {
			t.Fatalf(
				"rollback accepted a stale source after competing commit: %v",
				err,
			)
		}
	})
}

func holdCompetingDeployment(
	t *testing.T, repo *Repository, releaseID int64,
) (<-chan struct{}, context.CancelFunc, <-chan error) {
	t.Helper()
	created := make(chan struct{})
	ctx, commit := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		done <- repo.withQueueTx(t.Context(), func(txCtx context.Context, q *db.Queries) error {
			_, err := repo.createDeployment(txCtx, q,
				db.CreateDeploymentParams{
					ReleaseID: releaseID, EnvironmentID: 1, Status: "pending",
				})
			close(created)
			if err != nil {
				return err
			}
			<-ctx.Done()
			return nil
		})
	}()
	return created, commit, done
}

func TestVerificationRollbackAcrossDatabases(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		firstRepo, secondRepo := openDeploymentCreationEngine(
			t,
			newDeploymentCreationEngine(t, name),
		)
		box, err := secret.NewBox(make([]byte, 32))
		if err != nil {
			t.Fatal(err)
		}
		firstRepo.SetSecretBox(box)
		secondRepo.SetSecretBox(box)
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
	if _, err := firstRepo.UpdateEnvironment(
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
	plain, err := firstRepo.DecryptVerificationTarget(check.Target)
	if err != nil || plain != "https://example.com/health" {
		t.Fatal("encrypted verification target did not survive rollback")
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

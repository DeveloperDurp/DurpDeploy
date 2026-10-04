package repository

import (
	"context"
	"sync"
	"testing"

	"durpdeploy/internal/db"
)

func TestEnvironmentQueueConcurrentAdmission(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		// Given: two database connections and different releases in one environment.
		first, second := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		ctx := t.Context()
		release, err := first.Queries.CreateRelease(ctx, db.CreateReleaseParams{
			ProjectID: 1, Version: "v2", StepsJson: "[]",
		})
		if err != nil {
			t.Fatal(err)
		}
		start := make(chan struct{})
		var wg sync.WaitGroup
		failures := make(chan error, 8)
		// When: requests race across both connections and releases.
		for i := range 8 {
			wg.Go(func() {
				<-start
				repo, releaseID := first, int64(1)
				if i%2 == 1 {
					repo, releaseID = second, release.ID
				}
				_, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
					ReleaseID: releaseID, EnvironmentID: 1, Status: "pending",
				})
				failures <- err
			})
		}
		close(start)
		wg.Wait()
		close(failures)
		// Then: every request persists and exactly one owns the environment.
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		var admitted, queued int
		if err := first.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM deployments WHERE status='pending'`).Scan(&admitted); err != nil {
			t.Fatal(err)
		}
		if err := first.DB.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM deployments WHERE status='queued'`).Scan(&queued); err != nil {
			t.Fatal(err)
		}
		if admitted != 1 || queued != 7 {
			t.Fatalf("admitted=%d queued=%d, want 1 and 7", admitted, queued)
		}
	})
}

func TestEnvironmentQueueFIFOApprovalCancellationAndRecovery(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		// Given: approval work, an owner, and two queued requests.
		repo, restarted := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		ctx := t.Context()
		create := func(status string) db.Deployment {
			t.Helper()
			result, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: 1, Status: status,
			})
			if err != nil {
				t.Fatal(err)
			}
			return result.Deployment
		}
		approval := create("pending_approval")
		owner := create("pending")
		cancelled := create("pending")
		next := create("pending")
		if owner.Status != "pending" || next.Status != "queued" {
			t.Fatalf("owner=%s next=%s", owner.Status, next.Status)
		}
		otherEnvironment, err := repo.Queries.CreateEnvironment(ctx,
			db.CreateEnvironmentParams{Name: "independent"})
		if err != nil {
			t.Fatal(err)
		}
		independent, err := repo.CreateDeployment(
			ctx,
			db.CreateDeploymentParams{
				ReleaseID: 1, EnvironmentID: otherEnvironment.ID, Status: "pending",
			},
		)
		if err != nil || independent.Deployment.Status != "pending" {
			t.Fatalf("independent admission=%+v: %v", independent, err)
		}
		if err := repo.CancelQueuedDeployment(ctx, cancelled.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.ApproveDeployment(ctx, db.CreateApprovalParams{
			DeploymentID: approval.ID, ApprovedBy: "admin",
		}); err != nil {
			t.Fatal(err)
		}
		// When: another connection recovers the queue and completes its owner.
		if err := restarted.ReconcileDeploymentQueues(ctx); err != nil {
			t.Fatal(err)
		}
		finishQueueFixture(t, restarted, owner.ID, "succeeded")
		// Then: approval retains creation order, without preempting the owner.
		active, err := repo.Queries.GetEnvironmentDeploymentSlot(ctx, 1)
		if err != nil || active != approval.ID {
			t.Fatalf("active=%d, want %d: %v", active, approval.ID, err)
		}
		position, err := repo.Queries.GetDeploymentQueuePosition(ctx, next.ID)
		if err != nil || position != 1 {
			t.Fatalf("position=%d, want 1: %v", position, err)
		}
		finishQueueFixture(t, restarted, approval.ID, "failed")
		active, err = repo.Queries.GetEnvironmentDeploymentSlot(ctx, 1)
		if err != nil || active != next.ID {
			t.Fatalf("active=%d, want %d: %v", active, next.ID, err)
		}
		d, err := repo.Queries.GetDeployment(ctx, cancelled.ID)
		if err != nil || d.Status != "cancelled" {
			t.Fatalf("cancelled status=%s: %v", d.Status, err)
		}
	})
}

func TestEnvironmentQueueKeepsUnconfirmedCleanup(t *testing.T) {
	// Given: an environment with unconfirmed local cleanup and queued work.
	repo, _ := openDeploymentCreationEngine(t,
		newDeploymentCreationEngine(t, "SQLite"))
	ctx := t.Context()
	first, err := repo.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.CreateDeployment(ctx, db.CreateDeploymentParams{
		ReleaseID: 1, EnvironmentID: 1, Status: "pending",
	})
	if err != nil {
		t.Fatal(err)
	}
	// When: cleanup cannot confirm that execution stopped.
	finishQueueFixture(t, repo, first.Deployment.ID, "cleanup_unconfirmed")
	// Then: the environment remains owned by the uncertain deployment.
	active, err := repo.Queries.GetEnvironmentDeploymentSlot(ctx, 1)
	if err != nil || active != first.Deployment.ID {
		t.Fatalf("active=%d, want %d: %v", active, first.Deployment.ID, err)
	}
}

func finishQueueFixture(
	t *testing.T, repo *Repository, id int64, status string,
) {
	t.Helper()
	if err := repo.WithDeploymentTx(t.Context(), id, func(ctx context.Context, q *db.Queries) error {
		return q.UpdateDeploymentStatus(context.WithoutCancel(t.Context()),
			db.UpdateDeploymentStatusParams{ID: id, Status: status})
	}); err != nil {
		t.Fatal(err)
	}
}

func TestEnvironmentQueueAuditFailureDoesNotUndoState(t *testing.T) {
	forEachDeploymentCreationEngine(t, func(t *testing.T, name string) {
		repo, _ := openDeploymentCreationEngine(t,
			newDeploymentCreationEngine(t, name))
		if _, err := repo.DB.ExecContext(t.Context(),
			`DROP TABLE audit_log`); err != nil {
			t.Fatal(err)
		}
		create := func() db.Deployment {
			t.Helper()
			result, err := repo.CreateDeployment(t.Context(),
				db.CreateDeploymentParams{
					ReleaseID: 1, EnvironmentID: 1, Status: "pending",
				})
			if err != nil {
				t.Fatal(err)
			}
			return result.Deployment
		}
		owner, next := create(), create()
		if err := repo.CancelQueuedDeployment(t.Context(), owner.ID); err != nil {
			t.Fatal(err)
		}
		persisted, err := repo.Queries.GetDeployment(t.Context(), next.ID)
		if err != nil || persisted.Status != "pending" {
			t.Fatalf("next=%+v error=%v", persisted, err)
		}
	})
}

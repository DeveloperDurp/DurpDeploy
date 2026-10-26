package runner

import (
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestShutdownDrainsTerminalRetries(t *testing.T) {
	for _, scenario := range []struct {
		name, requested, expected string
		cancelled                 bool
	}{
		{"success", "succeeded", "succeeded", false},
		{"failure", "failed", "failed", false},
		{"cancellation", "succeeded", "cancelled", true},
		{"cleanup uncertainty", "cleanup_unconfirmed", "cleanup_unconfirmed", true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			r, repo, dep := terminalShutdownFixture(t)
			cancelCtx, cancel := context.WithCancel(t.Context())
			defer cancel()
			if _, persisted := r.persistCompletion(t.Context(), cancelCtx,
				dep.ID, scenario.requested,
				scenario.requested != "cleanup_unconfirmed"); persisted {
				t.Fatal("terminal write unexpectedly succeeded")
			}
			if scenario.cancelled {
				cancel()
			}
			done := make(chan struct{})
			go func() {
				r.KillAll()
				close(done)
			}()
			select {
			case <-done:
				t.Error("shutdown returned before the terminal write persisted")
			case <-time.After(50 * time.Millisecond):
			}
			if _, err := repo.DB.ExecContext(t.Context(),
				"DROP TRIGGER reject_shutdown_terminal"); err != nil {
				t.Fatal(err)
			}
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal(
					"shutdown did not finish after database access returned",
				)
			}
			stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
			if err != nil || stored.Status != scenario.expected ||
				!stored.FinishedAt.Valid {
				t.Fatalf("deployment after shutdown: %+v %v", stored, err)
			}
		})
	}
}

func TestShutdownTerminalRetryDeadline(t *testing.T) {
	r, repo, dep := terminalShutdownFixture(t)
	if _, persisted := r.persistCompletion(t.Context(), t.Context(),
		dep.ID, "succeeded", true); persisted {
		t.Fatal("terminal write unexpectedly succeeded")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		r.killAll(ctx)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("shutdown did not cancel its outstanding retry")
	}
	if ctx.Err() == nil {
		t.Fatal("shutdown returned before its retry deadline")
	}
	if _, err := repo.DB.ExecContext(t.Context(),
		"DROP TRIGGER reject_shutdown_terminal"); err != nil {
		t.Fatal(err)
	}
	// A retry must not write after shutdown has returned and DB close is safe.
	time.Sleep(1100 * time.Millisecond)
	stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil || stored.Status != "running" || stored.FinishedAt.Valid {
		t.Fatalf("shutdown lost durable recovery state: %+v %v", stored, err)
	}
	if _, err := repo.Queries.FailOrphanedDeployments(t.Context(),
		sql.NullInt64{Int64: time.Now().Unix(), Valid: true}); err != nil {
		t.Fatal(err)
	}
	stored, err = repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil || stored.Status != "failed" || !stored.FinishedAt.Valid {
		t.Fatalf("startup recovery failed: %+v %v", stored, err)
	}
}

func TestShutdownClosesTerminalRetryAdmission(t *testing.T) {
	r, repo, dep := terminalShutdownFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var work sync.WaitGroup
	work.Go(func() {
		r.persistCompletion(ctx, ctx, dep.ID, "succeeded", true)
	})
	work.Go(func() { r.killAll(ctx) })
	work.Wait()
	// A delayed Run failure after shutdown cannot launch another worker.
	r.persistCompletion(ctx, ctx, dep.ID, "failed", true)
	r.mu.Lock()
	remaining := len(r.completionCancels)
	r.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("shutdown left %d retry workers registered", remaining)
	}
	if _, err := repo.DB.ExecContext(t.Context(),
		"DROP TRIGGER reject_shutdown_terminal"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil || stored.Status != "running" || stored.FinishedAt.Valid {
		t.Fatalf("late completion changed recovery state: %+v %v", stored, err)
	}
}

func terminalShutdownFixture(t *testing.T) (
	*DeploymentRunner, *repository.Repository, db.Deployment,
) {
	t.Helper()
	r, repo, _ := podmanFixture(t, "")
	dep := stagingDeployment(t, repo)
	if err := repo.Queries.UpdateDeploymentStatus(t.Context(), db.UpdateDeploymentStatusParams{
		ID: dep.ID, Status: "running",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(), `
CREATE TRIGGER reject_shutdown_terminal BEFORE UPDATE OF status ON deployments
WHEN NEW.status IN ('succeeded','failed','cancelled','cleanup_unconfirmed')
BEGIN SELECT RAISE(FAIL, 'terminal write unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	return r, repo, dep
}

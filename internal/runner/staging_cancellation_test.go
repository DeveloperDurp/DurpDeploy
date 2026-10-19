package runner

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestStagingCreationDeadlineKeepsCleanupUnconfirmed(t *testing.T) {
	r, _, _ := podmanFixture(t, `
case "$3:$4" in volume:create) exec sleep 30;; esac
`)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	_, err := r.createStagingVolume(ctx, 1, deploymentStageMount)
	// Independent deadline timers can finish in either order.
	<-ctx.Done()
	if err == nil || ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("setup error=%v context=%v", err, ctx.Err())
	}
	// An absent resource is insufficient proof after interrupted creation.
	if err := r.cleanupArtifact(1); !errors.Is(err, errContainerCleanup) {
		t.Fatalf("interrupted creation cleanup error=%v", err)
	}
	if len(r.staging[1]) != 1 {
		t.Fatal("interrupted creation lost its cleanup identity")
	}
}

func TestCancellationWaitsForStagingCreation(t *testing.T) {
	for _, phase := range []string{"volume", "keeper"} {
		t.Run(phase, func(t *testing.T) {
			cancellationWaitsForStagingCreation(t, phase)
		})
	}
}

func cancellationWaitsForStagingCreation(t *testing.T, phase string) {
	t.Helper()
	r, repo, trace := podmanFixture(t, "")
	t.Setenv("STAGING_SETUP_PHASE", phase)
	// Keep engine setup pending until the test releases it.
	cli := `#!/bin/sh
case "$3:$4" in
volume:create) phase=volume;;
run:--detach) phase=keeper;;
rm:*|volume:rm)
  if [ -f "$PODMAN_TRACE.started" ] &&
     [ ! -f "$PODMAN_TRACE.complete" ]; then exit 7; fi
  exit 0;;
inspect:*) printf 'true\n'; exit 0;;
esac
if [ "${phase:-}" = "$STAGING_SETUP_PHASE" ]; then
  touch "$PODMAN_TRACE.started"
  exec sh -c '
    while [ ! -f "$PODMAN_TRACE.release" ]; do sleep 0.01; done
    touch "$PODMAN_TRACE.complete"
  '
fi
`
	require.NoError(
		t,
		os.WriteFile(r.engine.binary, []byte(cli), 0o700),
	)
	dep := stagingDeployment(t, repo)
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(t.Context(), dep.ID, dep.ReleaseID, dep.EnvironmentID)
	}()
	release := func() {
		if err := os.WriteFile(trace+".release", nil, 0o600); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() {
		release()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("staging creation did not finish")
		}
	})
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(trace + ".started"); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s creation did not start", phase)
		}
		time.Sleep(time.Millisecond)
	}
	require.NoError(t, r.Cancel(dep.ID))
	select {
	case <-done:
		t.Fatal("cleanup ran before staging creation settled")
	case <-time.After(100 * time.Millisecond):
	}
	release()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not finish after staging creation")
	}
	stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil || stored.Status != "cancelled" ||
		len(r.staging) != 0 {
		t.Fatalf(
			"deployment=%+v staging=%v error=%v",
			stored,
			r.staging,
			err,
		)
	}
	_, err = os.Stat(trace + ".complete")
	require.NoError(t, err, "engine operation did not settle")
}

func TestCancellationDuringStagingExportPreservesCancelledStatus(t *testing.T) {
	r, repo, trace := podmanFixture(t, `
case "$3" in
exec) printf exporting > "$PODMAN_TRACE"; exec sleep 30;;
esac
`)
	dep := stagingDeployment(t, repo)
	if _, err := repo.DB.ExecContext(
		t.Context(),
		`UPDATE deployment_step_sources SET steps_json=? WHERE deployment_id=?`,
		`[{"name":"producer","container_image":"example/worker:1"},{"name":"consumer","execution_target":"agent"}]`,
		dep.ID,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(
		t.Context(),
		`UPDATE deployment_steps SET execution_target='agent' WHERE deployment_id=? AND step_index=1`,
		dep.ID,
	); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(t.Context(), dep.ID, dep.ReleaseID, dep.EnvironmentID)
	}()
	t.Cleanup(func() {
		_ = r.Cancel(dep.ID)
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("staging export did not stop")
		}
	})
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(10 * time.Millisecond)
	defer tick.Stop()
	for {
		if _, err := os.Stat(trace); err == nil {
			break
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		select {
		case <-done:
			stored, _ := repo.Queries.GetDeployment(t.Context(), dep.ID)
			logs, _ := repo.Queries.ListDeploymentLogsByDeployment(
				t.Context(),
				dep.ID,
			)
			t.Fatalf(
				"deployment stopped before staging export: status=%s logs=%+v",
				stored.Status,
				logs,
			)
		case <-deadline.C:
			t.Fatal("staging export did not start")
		case <-tick.C:
		}
	}
	if err := r.Cancel(dep.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled staging export did not finish")
	}
	stored, err := repo.Queries.GetDeployment(t.Context(), dep.ID)
	if err != nil || stored.Status != "cancelled" || !stored.FinishedAt.Valid {
		t.Fatalf("cancelled staging deployment=%+v error=%v", stored, err)
	}
	if len(r.staging) != 0 || len(r.transfers) != 0 {
		t.Fatal("cancelled transfer retained runtime staging")
	}
}

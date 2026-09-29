//go:build linux

package runner

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestLocalCancellationRemovesRemoteAttempt(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'started\n' > "$PODMAN_TRACE"; exec sleep 30;;
rm) printf '%s\n' "$*" > "$PODMAN_TRACE.removed";;
esac
`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- r.runStepAttempt(ctx, localStepAttempt{
			deploymentID: 7, attempt: 1,
			step: deploymentStep{ContainerImage: "registry.example/worker:1", ScriptBody: "exit 0"},
			logWriter: &broadcastWriter{ctx: t.Context(), repo: repo, broker: r.broker,
				scrubber: NewScrubber(nil)},
		})
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(trace); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("Podman attempt did not start")
		}
	}
	// When
	cancel()
	// Then
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("cancelled attempt succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancelled attempt did not terminate")
	}
	removed, err := os.ReadFile(trace + ".removed")
	if err != nil ||
		!strings.Contains(
			string(removed),
			"--force --time=0 --ignore durpdeploy-7-1-",
		) {
		t.Fatalf("remote cleanup %q: %v", removed, err)
	}
}

func TestKillAllCancelsAndRemovesTrackedContainer(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'started' > "$PODMAN_TRACE"; exec sleep 30;;
rm) printf '%s\n' "$*" >> "$PODMAN_TRACE.removed";;
esac
`)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	r.RegisterCancel(11, cancel)
	result := make(chan error, 1)
	go func() {
		result <- r.runStepAttempt(ctx, localStepAttempt{
			deploymentID: 11, attempt: 1,
			step: deploymentStep{ContainerImage: "registry.example/worker:1"},
			logWriter: &broadcastWriter{ctx: t.Context(), repo: repo, broker: r.broker,
				scrubber: NewScrubber(nil)},
		})
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(trace); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("Podman attempt did not start")
		}
	}
	// When
	r.KillAll()
	// Then
	select {
	case err := <-result:
		if err == nil {
			t.Fatal("shutdown attempt succeeded")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown attempt did not terminate")
	}
	removed, err := os.ReadFile(trace + ".removed")
	if err != nil ||
		!strings.Contains(
			string(removed),
			"--force --time=0 --ignore durpdeploy-11-1-",
		) {
		t.Fatalf("shutdown cleanup %q: %v", removed, err)
	}
}

func TestLocalTimeoutRemovesRemoteAttempt(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf '%s\n' "$@" > "$PODMAN_TRACE"; exec sleep 30;;
rm) printf '%s\n' "$*" > "$PODMAN_TRACE.removed";;
esac
`)
	// When
	err := r.runStepAttempt(t.Context(), localStepAttempt{
		deploymentID: 9,
		attempt:      1,
		step: deploymentStep{
			ContainerImage: "registry.example/worker:1",
			TimeoutSeconds: 1,
		},
		logWriter: &broadcastWriter{
			ctx:      t.Context(),
			repo:     repo,
			broker:   r.broker,
			scrubber: NewScrubber(nil),
		},
	})
	// Then
	if err == nil {
		t.Fatal("timed-out attempt succeeded")
	}
	removed, err := os.ReadFile(trace + ".removed")
	if err != nil ||
		!strings.Contains(
			string(removed),
			"--force --time=0 --ignore durpdeploy-9-1-",
		) {
		t.Fatalf("timeout cleanup %q: %v", removed, err)
	}
	args, err := os.ReadFile(trace)
	if err != nil || !strings.Contains(string(args), "--timeout=1\n") {
		t.Fatalf("runtime timeout %q: %v", args, err)
	}
}

func TestLocalCancellationCleanupFailureDoesNotConfirmCancellation(
	t *testing.T,
) {
	// Given: the remote runtime does not acknowledge removal after a cancelled run.
	r, repo, trace := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'started\n' > "$PODMAN_TRACE"; exec sleep 30;;
rm) printf 'cleanup failed\n'; exit 7;;
esac
`)
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "e"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "v1",
			StepsJson: `[{"name":"local","container_image":"example.com/worker:1","script_body":"echo hi"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		r.Run(t.Context(), created.Deployment.ID, release.ID, env.ID)
	}()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(trace); err == nil {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("Podman attempt did not start")
		}
	}
	// When
	if err := r.Cancel(created.Deployment.ID); err != nil {
		t.Fatal(err)
	}
	// Then: a failed rm is not proof that the remote process stopped.
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("runner did not finish after cleanup failure")
	}
	stored, err := repo.Queries.GetDeployment(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil || stored.Status != "cleanup_unconfirmed" ||
		!stored.FinishedAt.Valid {
		t.Fatalf("unconfirmed cancellation = %+v: %v", stored, err)
	}
	logs, err := repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil || len(logs) != 1 ||
		!strings.Contains(logs[0].Line, "container cleanup unconfirmed") {
		t.Fatalf("unconfirmed cleanup logs = %+v: %v", logs, err)
	}
}

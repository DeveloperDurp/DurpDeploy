package runner

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestPodmanStartupRemovesNamespacedOrphans(t *testing.T) {
	// Given
	dir := t.TempDir()
	trace := filepath.Join(dir, "removed")
	cli := `#!/bin/sh
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) printf 'orphan-container-id\n';;
rm) printf '%s\n' "$*" > "$PODMAN_TRACE";;
esac
`
	if err := os.WriteFile(
		filepath.Join(dir, "podman"),
		[]byte(cli),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("PODMAN_TRACE", trace)
	t.Setenv(
		"DURPDEPLOY_PODMAN_URL",
		"ssh://executor@example.invalid/run/user/1234/podman/podman.sock",
	)
	t.Setenv("DURPDEPLOY_PODMAN_NAMESPACE", "test-suite")
	// When
	r := New(nil, nil)
	// Then
	if r.localErr != nil {
		t.Fatal(r.localErr)
	}
	removed, err := os.ReadFile(trace)
	if err != nil ||
		!strings.Contains(
			string(removed),
			"--force --time=0 --ignore orphan-container-id",
		) {
		t.Fatalf("orphan cleanup %q: %v", removed, err)
	}
}

func TestStartupSweepConfirmsOnlyUnconfirmedCleanupAfterRemoval(t *testing.T) {
	// Given
	dir := t.TempDir()
	cli := `#!/bin/sh
case "$3" in
info) if [ "$PODMAN_SWEEP_FAIL" = 2 ]; then
  printf '{"host":{"security":{"rootless":false}}}';
else printf '{"host":{"security":{"rootless":true}}}'; fi;;
ps) printf 'orphan-container-id\n';;
rm) if [ "$PODMAN_SWEEP_FAIL" = 1 ]; then exit 7; fi;;
esac
`
	if err := os.WriteFile(
		filepath.Join(dir, "podman"),
		[]byte(cli),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv(
		"DURPDEPLOY_PODMAN_URL",
		"ssh://executor@example.invalid/run/user/1234/podman/podman.sock",
	)
	t.Setenv("DURPDEPLOY_PODMAN_NAMESPACE", "test-suite")
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetMaxOpenConns(1)
	repo := repository.New(conn)
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
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	uncertain, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Status:        "cleanup_unconfirmed",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	other, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Status:        "succeeded",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := newPodmanEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	r := &DeploymentRunner{repo: repo, podman: endpoint}

	// When: the runtime reports rootful mode.
	t.Setenv("PODMAN_SWEEP_FAIL", "2")
	if err := r.reconcileAttempts(); err == nil {
		t.Fatal("rootful runtime was accepted")
	}
	stored, err := repo.Queries.GetDeployment(t.Context(), uncertain.ID)
	if err != nil || stored.Status != "cleanup_unconfirmed" {
		t.Fatalf("status after unsafe runtime = %+v: %v", stored, err)
	}
	// When: the runtime rejects removal.
	t.Setenv("PODMAN_SWEEP_FAIL", "1")
	if err := r.reconcileAttempts(); err == nil {
		t.Fatal("failed removal was accepted")
	}
	stored, err = repo.Queries.GetDeployment(t.Context(), uncertain.ID)
	if err != nil || stored.Status != "cleanup_unconfirmed" {
		t.Fatalf("status after failed removal = %+v: %v", stored, err)
	}
	// When: the next startup sweep removes labelled attempts.
	t.Setenv("PODMAN_SWEEP_FAIL", "0")
	if err := r.reconcileAttempts(); err != nil {
		t.Fatal(err)
	}

	// Then
	stored, err = repo.Queries.GetDeployment(t.Context(), uncertain.ID)
	if err != nil || stored.Status != "failed" || !stored.FinishedAt.Valid {
		t.Fatalf("confirmed status = %+v: %v", stored, err)
	}
	untouched, err := repo.Queries.GetDeployment(t.Context(), other.ID)
	if err != nil || untouched.Status != "succeeded" {
		t.Fatalf("other status = %+v: %v", untouched, err)
	}
}

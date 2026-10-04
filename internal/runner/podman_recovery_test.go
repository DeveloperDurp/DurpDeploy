package runner

import (
	"database/sql"
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
	binary := filepath.Join(dir, "podman")
	trace := filepath.Join(dir, "removed")
	cli := `#!/bin/sh
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) case "$*" in
  *"--filter=label=io.durpdeploy.gate-image"*) ;;
  *) printf 'orphan-container-id\n';;
esac;;
rm) printf '%s\n' "$*" > "$PODMAN_TRACE";;
volume)
  if [ "$4" = ls ]; then printf 'orphan-staging-volume\n'; fi
  if [ "$4" = rm ]; then printf '%s\n' "$*" > "$PODMAN_TRACE.volume"; fi;;
esac
`
	if err := os.WriteFile(
		binary,
		[]byte(cli),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODMAN_TRACE", trace)
	t.Setenv(
		"DURPDEPLOY_CONTAINER_URL",
		"unix:///run/podman/podman.sock",
	)
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test-suite")
	// When
	r := NewWithPodmanBinaryForTest(nil, nil, binary)
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
	volume, err := os.ReadFile(trace + ".volume")
	if err != nil ||
		!strings.Contains(string(volume), "volume rm orphan-staging-volume") {
		t.Fatalf("orphan staging cleanup %q: %v", volume, err)
	}
}

func TestStartupSweepConfirmsOnlyUnconfirmedCleanupAfterRemoval(t *testing.T) {
	// Given
	dir := t.TempDir()
	binary := filepath.Join(dir, "podman")
	cli := `#!/bin/sh
case "$3" in
info) if [ "$PODMAN_SWEEP_FAIL" = 2 ]; then
  printf '{"host":{"security":{"rootless":false}}}';
else printf '{"host":{"security":{"rootless":true}}}'; fi;;
ps) case "$*" in
  *"--filter=label=io.durpdeploy.gate-image"*) ;;
  *) printf 'orphan-container-id\n';;
esac;;
rm) if [ "$PODMAN_SWEEP_FAIL" = 1 ]; then exit 7; fi;;
esac
`
	if err := os.WriteFile(
		binary,
		[]byte(cli),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(
		"DURPDEPLOY_CONTAINER_URL",
		"unix:///run/podman/podman.sock",
	)
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test-suite")
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
	if _, err := repo.DB.ExecContext(t.Context(),
		"UPDATE deployments SET container_namespace = ? WHERE id = ?",
		"podman:test-suite", uncertain.ID); err != nil {
		t.Fatal(err)
	}
	legacy, err := repo.Queries.CreateDeployment(
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
	endpoint.binary = binary
	r := &DeploymentRunner{repo: repo, engine: endpoint}

	// When: the runtime rejects removal.
	t.Setenv("PODMAN_SWEEP_FAIL", "1")
	if err := r.reconcileAttempts(); err == nil {
		t.Fatal("failed removal was accepted")
	}
	stored, err := repo.Queries.GetDeployment(t.Context(), uncertain.ID)
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
	unknown, err := repo.Queries.GetDeployment(t.Context(), legacy.ID)
	if err != nil || unknown.Status != "cleanup_unconfirmed" {
		t.Fatalf("unknown namespace was confirmed = %+v: %v", unknown, err)
	}
}

func TestStartupSweepLeavesOtherNamespaceUnconfirmed(t *testing.T) {
	// Given: an earlier execution has an unconfirmed container in another namespace.
	binary := filepath.Join(t.TempDir(), "podman")
	cli := `#!/bin/sh
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
rm) exit 9;;
esac
`
	if err := os.WriteFile(binary, []byte(cli), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DURPDEPLOY_CONTAINER_URL",
		"unix:///run/podman/podman.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "new-namespace")
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
			ProjectID: project.ID, Version: "v1", StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: env.ID, Status: "running",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Queries.RecordContainerNamespace(t.Context(),
		db.RecordContainerNamespaceParams{
			DeploymentID: deployment.ID,
			Namespace:    sql.NullString{String: "old-namespace", Valid: true},
		}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Queries.UpdateDeploymentStatus(
		t.Context(),
		db.UpdateDeploymentStatusParams{
			ID: deployment.ID, Status: "cleanup_unconfirmed",
		},
	); err != nil {
		t.Fatal(err)
	}

	// When: startup sweeps the new namespace, where there are no containers.
	r := NewWithPodmanBinaryForTest(repo, NewLogBroker(), binary)

	// Then: the old execution cannot be treated as reconciled.
	if r.localErr != nil {
		t.Fatal(r.localErr)
	}
	stored, err := repo.Queries.GetDeployment(t.Context(), deployment.ID)
	if err != nil || stored.Status != "cleanup_unconfirmed" ||
		stored.ContainerNamespace.String != "old-namespace" {
		t.Fatalf("unconfirmed deployment = %+v: %v", stored, err)
	}
}

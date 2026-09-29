package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestPodmanEndpointIgnoresPoisonedPATH(t *testing.T) {
	// Given
	t.Setenv("PATH", t.TempDir())
	t.Setenv("DURPDEPLOY_PODMAN_URL",
		"ssh://executor@example.invalid/run/user/1234/podman/podman.sock")
	t.Setenv("DURPDEPLOY_PODMAN_NAMESPACE", "test-suite")

	// When
	endpoint, err := newPodmanEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	cmd := endpoint.command(context.Background(), "info")

	// Then
	if cmd.Path != "/usr/bin/podman" {
		t.Fatalf("Podman command path = %q", cmd.Path)
	}
}

func podmanFixture(
	t *testing.T,
	body string,
) (*DeploymentRunner, *repository.Repository, string) {
	t.Helper()
	dir := t.TempDir()
	trace := filepath.Join(dir, "trace")
	binary := filepath.Join(dir, "podman")
	cli := "#!/bin/sh\n" + body
	if err := os.WriteFile(
		binary,
		[]byte(cli),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODMAN_TRACE", trace)
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
	r := NewWithPodmanBinaryForTest(repo, NewLogBroker(), binary)
	if r.localErr != nil {
		t.Fatal(r.localErr)
	}
	return r, repo, trace
}

func TestLocalAttemptStreamsSelectedVariablesAndRedactsOutput(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run)
  printf '%s\n' "$@" > "$PODMAN_TRACE"
  printf '%s' "$SECRET" > "$PODMAN_TRACE.env"
  cat > "$PODMAN_TRACE.script"
  printf 'credential=topsecret\n';;
rm) ;;
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
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	dep, err := repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Status:        "running",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	writer := &broadcastWriter{
		ctx:          t.Context(),
		repo:         repo,
		broker:       r.broker,
		deploymentID: dep.ID,
		stepName:     "step",
		scrubber:     NewScrubber([]string{"topsecret"}),
	}
	// When
	err = r.runStepAttempt(
		t.Context(),
		localStepAttempt{
			deploymentID: dep.ID,
			attempt:      1,
			logWriter:    writer,
			step: deploymentStep{
				Name:           "step",
				ScriptBody:     "print('inside image')",
				Interpreter:    "python3",
				ContainerImage: "registry.example/test@sha256:abcd",
				VariableNames:  []string{"SECRET"},
			},
			environment: map[string]string{
				"SECRET":   "topsecret",
				"EXCLUDED": "not-selected",
			},
		},
	)
	// Then
	if err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	for _, flag := range []string{"--remote", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65534:65534", "--pids-limit=128", "--memory=256m", "--entrypoint=python3", "--timeout=300", "--log-driver=none", "--env\nSECRET\n"} {
		if !strings.Contains(string(args), flag) {
			t.Errorf("missing %s in %s", flag, args)
		}
	}
	if strings.Contains(string(args), "topsecret") ||
		strings.Contains(string(args), "not-selected") {
		t.Fatal("secret or unselected variable in argv")
	}
	contents, err := os.ReadFile(trace + ".env")
	if err != nil || string(contents) != "topsecret" {
		t.Fatalf("environment %q: %v", contents, err)
	}
	script, err := os.ReadFile(trace + ".script")
	if err != nil || string(script) != "print('inside image')" {
		t.Fatalf("script %q: %v", script, err)
	}
	var line string
	if err := repo.DB.QueryRowContext(t.Context(), "SELECT line FROM deployment_logs WHERE deployment_id=?", dep.ID).
		Scan(&line); err != nil ||
		strings.Contains(line, "topsecret") ||
		!strings.Contains(line, "[REDACTED]") {
		t.Fatalf("scrubbed line %q: %v", line, err)
	}
}

func TestLocalAttemptFailsClosedWithoutImageOrEndpoint(t *testing.T) {
	// Given
	r := &DeploymentRunner{localErr: os.ErrNotExist}
	// When
	err := r.runStepAttempt(
		t.Context(),
		localStepAttempt{step: deploymentStep{Name: "legacy"}},
	)
	// Then
	if err == nil || !strings.Contains(err.Error(), "no container image") {
		t.Fatalf("missing image: %v", err)
	}
}

func TestPodmanEndpointRejectsRootfulEngines(t *testing.T) {
	for _, endpoint := range []string{
		"unix:///run/podman/podman.sock",
		"ssh://root@example.invalid/run/podman/podman.sock",
		"ssh://user@example.invalid/run/podman/podman.sock",
	} {
		t.Run(endpoint, func(t *testing.T) {
			// Given
			t.Setenv("DURPDEPLOY_PODMAN_URL", endpoint)
			t.Setenv("DURPDEPLOY_PODMAN_NAMESPACE", "test")
			// When
			_, err := newPodmanEndpoint()
			// Then
			if err == nil {
				t.Fatal("accepted unsafe Podman endpoint")
			}
		})
	}
}

func TestPodmanEndpointAllowsSameHostRootlessAccount(t *testing.T) {
	for _, host := range []string{"localhost", "127.0.0.1", "[::1]"} {
		t.Run(host, func(t *testing.T) {
			t.Setenv(
				"DURPDEPLOY_PODMAN_URL",
				"ssh://executor@"+host+"/run/user/1234/podman/podman.sock",
			)
			t.Setenv("DURPDEPLOY_PODMAN_NAMESPACE", "test")
			if _, err := newPodmanEndpoint(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestDockerEndpointUsesFixedClientAndSSHHost(t *testing.T) {
	t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "docker")
	t.Setenv("DURPDEPLOY_CONTAINER_URL",
		"ssh://executor@example.invalid/run/user/1234/docker.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test")
	endpoint, err := newContainerEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	cmd := endpoint.command(t.Context(), "info")
	if cmd.Path != "/usr/bin/docker" ||
		strings.Join(cmd.Args[1:], " ") !=
			"--host=ssh://executor@example.invalid/run/user/1234/docker.sock info" {
		t.Fatalf("Docker command = %q", cmd.Args)
	}
}

func TestDockerEndpointRejectsRootAndSocketPaths(t *testing.T) {
	for _, endpoint := range []string{
		"ssh://root@example.invalid",
		"ssh://executor@example.invalid",
		"ssh://executor@example.invalid/var/run/docker.sock",
		"unix:///run/user/1234/docker.sock",
	} {
		t.Run(endpoint, func(t *testing.T) {
			t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "docker")
			t.Setenv("DURPDEPLOY_CONTAINER_URL", endpoint)
			t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test")
			if _, err := newContainerEndpoint(); err == nil {
				t.Fatal("accepted unsafe Docker endpoint")
			}
		})
	}
}

func TestDockerRootlessEngineIsAccepted(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
case "$2" in
info) printf '["name=rootless","name=seccomp"]';;
ps) ;;
esac
`), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "docker")
	t.Setenv("DURPDEPLOY_CONTAINER_URL",
		"ssh://executor@example.invalid/run/user/1234/docker.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test")
	if r := newRunner(nil, nil, binary); r.localErr != nil {
		t.Fatal(r.localErr)
	}
}

func TestPodmanRootfulEngineFailsClosed(t *testing.T) {
	// Given
	dir := t.TempDir()
	binary := filepath.Join(dir, "podman")
	if err := os.WriteFile(
		binary,
		[]byte(
			"#!/bin/sh\nprintf '{\"host\":{\"security\":{\"rootless\":false}}}'\n",
		),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(
		"DURPDEPLOY_PODMAN_URL",
		"ssh://executor@example.invalid/run/user/1234/podman/podman.sock",
	)
	t.Setenv("DURPDEPLOY_PODMAN_NAMESPACE", "test")
	// When
	r := NewWithPodmanBinaryForTest(nil, nil, binary)
	// Then
	if r.localErr == nil || !strings.Contains(r.localErr.Error(), "rootless") {
		t.Fatalf("rootful engine accepted: %v", r.localErr)
	}
}

func TestRunnerRetriesWithFreshContainers(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run)
  for arg do case "$arg" in --name=*) printf '%s\n' "$arg" >> "$PODMAN_TRACE";; esac; done
  count=$(wc -l < "$PODMAN_TRACE")
  if [ "$count" -eq 1 ]; then exit 7; fi;;
rm) ;;
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
			StepsJson: `[{"name":"retry","execution_target":"local","container_image":"example.com/worker:1","script_body":"exit 0","max_retries":1}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	// When
	r.Run(t.Context(), deployment.Deployment.ID, release.ID, env.ID)
	// Then
	stored, err := repo.Queries.GetDeployment(
		t.Context(),
		deployment.Deployment.ID,
	)
	if err != nil || stored.Status != "succeeded" {
		t.Fatalf("retry deployment %q: %v", stored.Status, err)
	}
	names, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(names))
	if len(lines) != 2 || lines[0] == lines[1] {
		t.Fatalf("attempt names: %q", names)
	}
}

func TestRunnerKeepsCleanupFailureUnconfirmedWithoutRetry(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'run\n' >> "$PODMAN_TRACE";;
rm) printf 'remove failed\n'; exit 7;;
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
			StepsJson: `[{"name":"local","container_image":"example.com/worker:1","max_retries":2}]`,
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

	// When
	r.Run(t.Context(), created.Deployment.ID, release.ID, env.ID)

	// Then
	stored, err := repo.Queries.GetDeployment(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil || stored.Status != "cleanup_unconfirmed" ||
		!stored.FinishedAt.Valid ||
		stored.ContainerNamespace.String != "podman:test-suite" {
		t.Fatalf("cleanup status = %+v: %v", stored, err)
	}
	runs, err := os.ReadFile(trace)
	if err != nil || string(runs) != "run\n" {
		t.Fatalf("unexpected automatic retry %q: %v", runs, err)
	}
}

func TestRunnerDoesNotStartInDifferentNamespace(t *testing.T) {
	// Given: a running deployment was associated with another namespace.
	r, repo, trace := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'started' > "$PODMAN_TRACE";;
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
			StepsJson: `[{"name":"local","container_image":"example.com/worker:1"}]`,
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
	if err := repo.Queries.UpdateDeploymentStatus(
		t.Context(),
		db.UpdateDeploymentStatusParams{
			ID: created.Deployment.ID, Status: "running",
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(),
		"UPDATE deployments SET container_namespace = ? WHERE id = ?",
		"old-namespace", created.Deployment.ID); err != nil {
		t.Fatal(err)
	}

	// When: a runner with a different namespace tries to execute that deployment.
	r.Run(t.Context(), created.Deployment.ID, release.ID, env.ID)

	// Then: execution stays blocked and Podman never runs a new attempt.
	stored, err := repo.Queries.GetDeployment(
		t.Context(),
		created.Deployment.ID,
	)
	if err != nil || stored.Status != "cleanup_unconfirmed" ||
		stored.ContainerNamespace.String != "old-namespace" {
		t.Fatalf("deployment = %+v: %v", stored, err)
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("unexpected Podman start: %v", err)
	}
}

func TestRunnerRejectsUnknownTargetBeforePodman(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'ran' > "$PODMAN_TRACE";;
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
			StepsJson: `[{"name":"unsafe","execution_target":"local","container_image":"example.com/worker:1"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Status:        "pending",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.DB.ExecContext(
		t.Context(),
		`UPDATE deployment_step_sources SET steps_json=? WHERE deployment_id=?`,
		`[{"name":"unsafe","execution_target":"unsupported","container_image":"example.com/worker:1"}]`,
		second.Deployment.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	// When
	r.Run(t.Context(), second.Deployment.ID, release.ID, env.ID)
	// Then
	stored, err := repo.Queries.GetDeployment(t.Context(), second.Deployment.ID)
	if err != nil || stored.Status != "failed" {
		t.Fatalf("unknown target deployment %q: %v", stored.Status, err)
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("Podman ran for unknown target: %v", err)
	}
}

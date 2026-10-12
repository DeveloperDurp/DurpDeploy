package runner

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestPodmanEndpointIgnoresPoisonedPATH(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "podman")
	t.Setenv("DURPDEPLOY_CONTAINER_URL",
		"unix:///run/podman/podman.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test-suite")

	// When
	endpoint, err := newPodmanEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	cmd := endpoint.command(context.Background(), "info")

	// Then
	if filepath.Base(cmd.Path) != "podman" {
		t.Fatalf("Podman command path = %q", cmd.Path)
	}
	if !slices.Contains(cmd.Env,
		"CONTAINER_HOST=unix:///run/podman/podman.sock") {
		t.Fatalf("Podman command environment = %q", cmd.Env)
	}
}

func TestContainerCommandIgnoresInheritedRemoteEndpoints(t *testing.T) {
	t.Setenv("DOCKER_CONTEXT", "remote-context")
	t.Setenv("DOCKER_HOST", "ssh://remote.invalid")
	t.Setenv("CONTAINER_CONNECTION", "remote-connection")
	t.Setenv("CONTAINER_HOST", "ssh://remote.invalid")
	endpoint := containerEndpoint{kind: "docker", binary: "/usr/bin/docker"}
	cmd := endpoint.command(t.Context(), "info")
	for _, entry := range cmd.Env {
		for _, name := range []string{
			"DOCKER_CONTEXT=", "DOCKER_HOST=", "CONTAINER_CONNECTION=",
			"CONTAINER_HOST=",
		} {
			if strings.HasPrefix(entry, name) {
				t.Fatalf("inherited remote endpoint %q", entry)
			}
		}
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
	// Step-focused fixtures also provide the deployment's idle staging keeper.
	cli := `#!/bin/sh
case "$3" in
inspect) printf 'true\n'; exit 0;;
run) for arg do case "$arg" in --detach) exit 0;; esac; done;;
esac
` + body
	if err := os.WriteFile(
		binary,
		[]byte(cli),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PODMAN_TRACE", trace)
	t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "podman")
	t.Setenv("DURPDEPLOY_CONTAINER_URL", "unix:///run/podman/podman.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test-suite")
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

func attemptLogWriter(
	t *testing.T,
	r *DeploymentRunner,
	repo *repository.Repository,
	id int64,
) *broadcastWriter {
	t.Helper()
	project, err := repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "attempt"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "attempt"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "attempt",
			StepsJson: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(), `INSERT INTO deployments(id,release_id,environment_id,status) VALUES(?,?,?,'running')`, id, release.ID, env.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.DB.ExecContext(t.Context(), `INSERT INTO deployment_step_sources(deployment_id,steps_json) VALUES(?,'[]')`, id); err != nil {
		t.Fatal(err)
	}
	return &broadcastWriter{ctx: t.Context(), repo: repo, broker: r.broker,
		deploymentID: id, stepIndex: sql.NullInt64{Valid: true}, scrubber: NewScrubber(nil)}
}

func TestLocalAttemptStreamsSelectedVariablesAndRedactsOutput(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$3" in
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
	if _, err := repo.DB.ExecContext(t.Context(), `INSERT INTO deployment_step_sources(deployment_id,steps_json) VALUES(?,'[]')`, dep.ID); err != nil {
		t.Fatal(err)
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
	for _, flag := range []string{"--remote", "--pull=missing", "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--user=65534:65534", "--tmpfs=/tmp:rw,nosuid,size=64m", "--env=HOME=/tmp", "--env=TERM=dumb", "--pids-limit=128", "--entrypoint=python3", "--timeout=300", "--log-driver=none", "--env\nSECRET\n"} {
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
	if err := repo.DB.QueryRowContext(t.Context(), "SELECT line FROM deployment_logs WHERE deployment_id=? AND step_state IS NULL", dep.ID).
		Scan(&line); err != nil ||
		strings.Contains(line, "topsecret") ||
		!strings.Contains(line, "[REDACTED]") {
		t.Fatalf("scrubbed line %q: %v", line, err)
	}
}

func TestEmbeddedAgentCanBeDisabled(t *testing.T) {
	t.Setenv("DURPDEPLOY_EMBEDDED_AGENT_ENABLED", "false")
	_, err := newContainerEndpointWith("podman", "/usr/bin/podman")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("disabled embedded agent: %v", err)
	}
}

func TestEmbeddedAgentRejectsInvalidToggle(t *testing.T) {
	t.Setenv("DURPDEPLOY_EMBEDDED_AGENT_ENABLED", "sometimes")
	_, err := newContainerEndpointWith("podman", "/usr/bin/podman")
	if err == nil || !strings.Contains(err.Error(), "true or false") {
		t.Fatalf("invalid embedded agent toggle: %v", err)
	}
}

func TestRuntimeDetectionSkipsUnavailableDockerClient(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "podman")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("DOCKER_HOST", "unix:///run/user/1000/podman/podman.sock")
	if got := detectContainerRuntime(); got != "podman" {
		t.Fatalf("detected runtime = %q, want podman", got)
	}
}

func TestLocalAttemptFailsClosedWithoutImageOrEndpoint(t *testing.T) {
	// Given
	r, repo, _ := podmanFixture(t, "")
	r.localErr = os.ErrNotExist
	// When
	err := r.runStepAttempt(
		t.Context(),
		localStepAttempt{
			step:      deploymentStep{Name: "legacy"},
			logWriter: attemptLogWriter(t, r, repo, 0),
		},
	)
	// Then
	if err == nil || !strings.Contains(err.Error(), "no container image") {
		t.Fatalf("missing image: %v", err)
	}
}

func TestContainerEndpointRejectsNonUnixURLs(t *testing.T) {
	for _, endpoint := range []string{
		"ssh://root@example.invalid/run/podman/podman.sock",
		"tcp://127.0.0.1:2375",
		"unix://relative.sock",
	} {
		t.Run(endpoint, func(t *testing.T) {
			// Given
			t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "podman")
			t.Setenv("DURPDEPLOY_CONTAINER_URL", endpoint)
			t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test")
			// When
			_, err := newPodmanEndpoint()
			// Then
			if err == nil {
				t.Fatal("accepted unsafe Podman endpoint")
			}
		})
	}
}

func TestPodmanEndpointAllowsUnixSocket(t *testing.T) {
	t.Setenv("DURPDEPLOY_CONTAINER_URL", "unix:///run/podman/podman.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test")
	if _, err := newPodmanEndpoint(); err != nil {
		t.Fatal(err)
	}
}

func TestDockerEndpointUsesUnixSocket(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "docker")
	t.Setenv("DURPDEPLOY_CONTAINER_URL",
		"unix:///var/run/docker.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test")
	endpoint, err := newContainerEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	cmd := endpoint.command(t.Context(), "info")
	if filepath.Base(cmd.Path) != "docker" ||
		strings.Join(cmd.Args[1:], " ") !=
			"--host=unix:///var/run/docker.sock info" {
		t.Fatalf("Docker command = %q", cmd.Args)
	}
	if got := strings.Join(
		endpoint.removeArgs("attempt"),
		" ",
	); got != "rm --force --volumes attempt" {
		t.Fatalf("Docker cleanup = %q", got)
	}
	if err := endpoint.removalError(
		[]byte("Error response from daemon: No such container: attempt"),
		os.ErrNotExist,
	); err != nil {
		t.Fatalf("absent Docker container cleanup: %v", err)
	}
}

func TestDockerRejectsImageDeclaredVolumes(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
printf '{"/data":{}}'
`), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &DeploymentRunner{engine: containerEndpoint{
		kind: "docker", binary: binary, url: "unix:///var/run/docker.sock",
	}}
	err := runner.rejectDockerImageVolumes(t.Context(), "example/image:1")
	if err == nil || !strings.Contains(err.Error(), "writable volumes") {
		t.Fatalf("image-declared volumes accepted: %v", err)
	}
}

func TestDockerPullsMissingImageBeforeVolumeInspection(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	state := filepath.Join(dir, "pulled")
	script := fmt.Sprintf(`#!/bin/sh
case " $* " in
*" pull "*)
  : > %q
  exit 0
  ;;
esac
if [ ! -f %q ]; then
  exit 1
fi
printf 'null'
`, state, state)
	if err := os.WriteFile(binary, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &DeploymentRunner{engine: containerEndpoint{
		kind: "docker", binary: binary, url: "unix:///var/run/docker.sock",
	}}
	if err := runner.rejectDockerImageVolumes(
		t.Context(), "example/image:1",
	); err != nil {
		t.Fatalf("inspect missing image after pull: %v", err)
	}
	if _, err := os.Stat(state); err != nil {
		t.Fatalf("image was not pulled: %v", err)
	}
}

func TestDockerImageInspectionUsesStepTimeout(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	if err := os.WriteFile(binary, []byte(`#!/bin/sh
exec sleep 5
`), 0o700); err != nil {
		t.Fatal(err)
	}
	r, repo, _ := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
esac
`)
	r.engine = containerEndpoint{
		kind: "docker", binary: binary,
		url: "unix:///var/run/docker.sock",
	}
	writer := attemptLogWriter(t, r, repo, 0)
	started := time.Now()
	err := r.runStepAttempt(t.Context(), localStepAttempt{
		step: deploymentStep{
			Name: "inspect", ContainerImage: "example/image:1",
			TimeoutSeconds: 1,
		},
		logWriter: writer,
	})
	if err == nil || time.Since(started) > 3*time.Second {
		t.Fatalf("inspection timeout after %s: %v", time.Since(started), err)
	}
}

func TestDockerEndpointRejectsNonUnixURLs(t *testing.T) {
	for _, endpoint := range []string{
		"ssh://root@example.invalid",
		"ssh://executor@example.invalid",
		"tcp://127.0.0.1:2375",
		"unix://relative.sock",
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

func TestDockerEngineIsAccepted(t *testing.T) {
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
		"unix:///var/run/docker.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test")
	if r := newRunner(nil, nil, binary); r.localErr != nil {
		t.Fatal(r.localErr)
	}
}

func TestPodmanEngineDoesNotRequireRootlessMode(t *testing.T) {
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
		"DURPDEPLOY_CONTAINER_URL",
		"unix:///run/podman/podman.sock",
	)
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "test")
	// When
	r := NewWithPodmanBinaryForTest(nil, nil, binary)
	// Then
	if r.localErr != nil {
		t.Fatalf("container engine rejected: %v", r.localErr)
	}
}

func TestRunnerRetriesWithFreshContainers(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$3" in
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
case "$3" in
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

func TestRunnerFailsWhenNamespaceCannotBeRecordedBeforeLaunch(t *testing.T) {
	r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'started' > "$PODMAN_TRACE";;
esac
`)
	project, err := repo.Queries.CreateProject(
		t.Context(), db.CreateProjectParams{Name: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := repo.Queries.CreateEnvironment(
		t.Context(), db.CreateEnvironmentParams{Name: "e"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "v1",
			StepsJson: `[{"name":"local","container_image":"example/image:1"}]`,
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
	if _, err := repo.DB.ExecContext(t.Context(), `
CREATE TRIGGER reject_container_namespace
BEFORE UPDATE OF container_namespace ON deployments
WHEN NEW.container_namespace IS NOT NULL
BEGIN
  SELECT RAISE(IGNORE);
END`); err != nil {
		t.Fatal(err)
	}

	r.Run(t.Context(), created.Deployment.ID, release.ID, env.ID)

	stored, err := repo.Queries.GetDeployment(
		t.Context(), created.Deployment.ID,
	)
	if err != nil || stored.Status != "failed" ||
		stored.ContainerNamespace.Valid {
		t.Fatalf("deployment = %+v: %v", stored, err)
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("container started before namespace record: %v", err)
	}
}

func TestRunnerDoesNotStartInDifferentNamespace(t *testing.T) {
	// Given: a running deployment was associated with another namespace.
	r, repo, trace := podmanFixture(t, `
case "$3" in
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
case "$3" in
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

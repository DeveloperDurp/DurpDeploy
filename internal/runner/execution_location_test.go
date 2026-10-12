package runner

import (
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestLocalAttemptLogsPhysicalRuntimeLocationBeforeOutput(t *testing.T) {
	// Given: the selected runtime reports its host, independently of the server.
	r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"hostname":"physical-runner","security":{"rootless":true}}}';;
ps) ;;
run) printf '%s\n' "$@" > "$PODMAN_TRACE"; printf 'script-output\n';;
rm) ;;
esac
`)
	request := localStepAttempt{
		step: deploymentStep{
			Name: "Deploy", ContainerImage: "example.com/worker:1",
		},
		logWriter: attemptLogWriter(t, r, repo, 0),
		attempt:   2,
	}

	// When: the attempt executes.
	if err := r.runStepAttempt(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	// Then: context precedes output and identifies the actual runtime command.
	logs, err := repo.Queries.ListDeploymentLogsAfter(
		t.Context(),
		db.ListDeploymentLogsAfterParams{
			DeploymentID: request.logWriter.deploymentID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	location, output := -1, -1
	for index, log := range logs {
		if strings.HasPrefix(log.Line, "Execution location:") {
			location = index
		}
		if log.Line == "script-output" {
			output = index
		}
	}
	if location < 0 || output <= location {
		t.Fatalf("execution context missing or after script output: %+v", logs)
	}
	line := logs[location].Line
	for _, field := range []string{
		`target=server`, `host="physical-runner"`, `mode=container`,
		`runtime="podman"`, `image="example.com/worker:1"`,
		`interpreter="bash"`, `attempt=2`,
	} {
		if !strings.Contains(line, field) {
			t.Errorf("execution location missing %q: %s", field, line)
		}
	}
	args, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	container := ""
	for _, arg := range strings.Split(string(args), "\n") {
		if strings.HasPrefix(arg, "--name=") {
			container = strings.TrimPrefix(arg, "--name=")
		}
	}
	if container == "" || !strings.Contains(line, `container="`+container+`"`) {
		t.Fatalf("logged container differs from executed container: %s", line)
	}
}

func TestLocalAttemptHostnameLookupDoesNotConsumeStepTimeout(t *testing.T) {
	// Given: hostname discovery takes longer than the script timeout.
	r, repo, _ := podmanFixture(t, `
case "$3" in
info) sleep 1.2; printf '{"host":{"hostname":"physical-runner","security":{"rootless":true}}}';;
ps) ;;
run) printf 'script-output\n';;
rm) ;;
esac
`)
	request := localStepAttempt{
		step: deploymentStep{
			Name: "Deploy", ContainerImage: "example.com/worker:1",
			TimeoutSeconds: 1,
		},
		logWriter: attemptLogWriter(t, r, repo, 0),
		attempt:   1,
	}

	// When: the script executes after bounded metadata discovery.
	if err := r.runStepAttempt(t.Context(), request); err != nil {
		t.Fatal(err)
	}

	// Then: its execution budget remains available.
	logs, err := repo.Queries.ListDeploymentLogsAfter(
		t.Context(), db.ListDeploymentLogsAfterParams{DeploymentID: 0},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, log := range logs {
		if log.Line == "script-output" {
			return
		}
	}
	t.Fatalf("script did not execute: %+v", logs)
}

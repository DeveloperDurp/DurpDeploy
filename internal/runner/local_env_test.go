package runner

import (
	"errors"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/containerenv"
)

func TestLocalAttemptRejectsMalformedDefaultEnvironment(t *testing.T) {
	for _, name := range []string{"BAD\x00NAME", "BAD=NAME", "bad name", "2NAME", strings.Repeat("A", 256)} {
		t.Run(name, func(t *testing.T) {
			r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{}';;
ps|rm|volume) ;;
run) printf started > "$PODMAN_TRACE";;
esac
`)
			err := r.runStepAttempt(t.Context(), localStepAttempt{
				step: deploymentStep{
					ContainerImage: "example.com/worker:1",
				},
				logWriter:   attemptLogWriter(t, r, repo, 0),
				environment: map[string]string{name: "private-value"},
			})
			if !errors.Is(err, containerenv.ErrEnvironment) {
				t.Fatalf("malformed environment: %v", err)
			}
			if _, err := os.Stat(trace); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("workload started: %v", err)
			}
		})
	}
}

func TestLocalEnvironmentExplicitSelectionIgnoresUnselectedEntries(
	t *testing.T,
) {
	request := localStepAttempt{
		step: deploymentStep{VariableNames: []string{"Mixed_2", "_EMPTY"}},
		environment: map[string]string{
			"Mixed_2": "first\nsecond", "_EMPTY": "", "bad name": "private\x00value",
		},
	}
	entries, _, err := request.containerEnvironment()
	if err != nil || len(entries) != 2 ||
		entries[0] != "Mixed_2=first\nsecond" ||
		entries[1] != "_EMPTY=" {
		t.Fatalf("selected entries=%q error=%v", entries, err)
	}
}

func TestLocalAttemptRejectsMalformedSelectedNameWithoutLeaking(t *testing.T) {
	r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{}';;
ps|rm|volume) ;;
run) printf started > "$PODMAN_TRACE";;
esac
`)
	err := r.runStepAttempt(t.Context(), localStepAttempt{
		step: deploymentStep{
			ContainerImage: "example.com/worker:1",
			VariableNames:  []string{"private=NAME"},
		},
		logWriter:   attemptLogWriter(t, r, repo, 0),
		environment: map[string]string{"private=NAME": "private-value"},
	})
	if !errors.Is(err, containerenv.ErrEnvironment) {
		t.Fatalf("malformed selected environment: %v", err)
	}
	logs, logErr := repo.Queries.ListDeploymentLogsByDeployment(t.Context(), 0)
	var output strings.Builder
	for _, log := range logs {
		output.WriteString(log.Line)
	}
	if logErr != nil ||
		strings.Count(output.String(), "invalid execution environment") != 1 ||
		strings.Contains(output.String(), "private") {
		t.Fatalf("unsafe validation logs=%+v error=%v", logs, logErr)
	}
	if _, err := os.Stat(trace); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("workload started: %v", err)
	}
}

func TestLocalAttemptPassesAllVariablesByDefault(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run)
  printf '%s\n' "$@" > "$PODMAN_TRACE"
  printf '%s' "$TOKEN" > "$PODMAN_TRACE.value";;
rm) ;;
esac
`)
	// When
	err := r.runStepAttempt(t.Context(), localStepAttempt{
		step: deploymentStep{
			ContainerImage: "example.com/worker:1",
		},
		logWriter: attemptLogWriter(t, r, repo, 0),
		environment: map[string]string{
			"DOCKER_HOST": "tcp://attacker.invalid",
			"MODE":        "deploy",
			"TOKEN":       "first\nsecond\rthird",
		},
	})
	// Then
	if err != nil {
		t.Fatal(err)
	}
	value, err := os.ReadFile(trace + ".value")
	if err != nil || string(value) != "first\nsecond\rthird" {
		t.Fatalf("selected variable %q: %v", value, err)
	}
	args, err := os.ReadFile(trace)
	if err != nil || !strings.Contains(string(args), "--env\nMODE\n") ||
		!strings.Contains(string(args), "--env\nTOKEN\n") ||
		strings.Contains(string(args), "--env\nDOCKER_HOST\n") ||
		strings.Contains(string(args), "first") {
		t.Fatalf("variable exposed in argv %q: %v", args, err)
	}
}

func TestLocalAttemptRejectsNULSelectedVariable(t *testing.T) {
	// Given
	r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'started' > "$PODMAN_TRACE";;
esac
`)
	// When
	err := r.runStepAttempt(t.Context(), localStepAttempt{
		step: deploymentStep{
			ContainerImage: "example.com/worker:1",
			VariableNames:  []string{"TOKEN"},
		},
		logWriter:   attemptLogWriter(t, r, repo, 0),
		environment: map[string]string{"TOKEN": "first\x00second"},
	})
	// Then
	if err == nil {
		t.Fatal("accepted NUL in selected variable")
	}
	if _, err := os.Stat(trace); !os.IsNotExist(err) {
		t.Fatalf("Podman started for invalid variable: %v", err)
	}
}

func TestPowerShellAttemptUsesCleanCommandMode(t *testing.T) {
	r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf '%s\n' "$@" > "$PODMAN_TRACE";;
rm) ;;
esac
`)
	err := r.runStepAttempt(t.Context(), localStepAttempt{
		step: deploymentStep{
			ContainerImage: "harbor.durp.info/microsoft/powershell:latest",
			Interpreter:    "pwsh",
			ScriptBody:     "Get-Host",
		},
		logWriter: attemptLogWriter(t, r, repo, 0),
	})
	if err != nil {
		t.Fatal(err)
	}
	args, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	text := string(args)
	for _, flag := range []string{
		"--env=HOME=/tmp", "--env=TERM=dumb", "--entrypoint=pwsh",
		"-NoLogo", "-NoProfile", "-NonInteractive", "-Command\n-\n",
	} {
		if !strings.Contains(text, flag) {
			t.Errorf("missing %q in %q", flag, text)
		}
	}
	if strings.Contains(text, "-File") {
		t.Fatalf("interactive file mode in %q", text)
	}
}

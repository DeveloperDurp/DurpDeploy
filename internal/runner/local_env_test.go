package runner

import (
	"os"
	"strings"
	"testing"
)

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
		logWriter: &broadcastWriter{
			ctx:      t.Context(),
			repo:     repo,
			broker:   r.broker,
			scrubber: NewScrubber(nil),
		},
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
		logWriter: &broadcastWriter{
			ctx:      t.Context(),
			repo:     repo,
			broker:   r.broker,
			scrubber: NewScrubber(nil),
		},
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

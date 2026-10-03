package runner

import (
	"os"
	"strings"
	"testing"
)

func TestVerificationRejectsExplicitStartupVariables(t *testing.T) {
	for _, name := range []string{"BASH_ENV", "ENV", "SHELLOPTS", "BASHOPTS",
		"CDPATH", "GLOBIGNORE", "PS4", "LD_PRELOAD", "LD_LIBRARY_PATH",
		"GCONV_PATH", "GLIBC_TUNABLES", "PATH"} {
		t.Run(name, func(t *testing.T) {
			r, repo, trace := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'started' > "$PODMAN_TRACE";;
esac
`)
			err := r.runStepAttempt(t.Context(), localStepAttempt{
				step: deploymentStep{
					ContainerImage: "example.com/worker:1",
					VariableNames:  []string{name},
				},
				logWriter: &broadcastWriter{
					ctx: t.Context(), repo: repo, broker: r.broker,
					scrubber: NewScrubber(nil),
				},
				environment: map[string]string{
					name: "untrusted",
				},
				verification: true,
			})
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatal(
					"verification accepted an explicitly selected startup variable",
				)
			}
			if _, err := os.Stat(trace); !os.IsNotExist(err) {
				t.Fatal("verification started with an unsafe variable")
			}
		})
	}
}

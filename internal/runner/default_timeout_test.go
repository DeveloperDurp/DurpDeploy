package runner

import (
	"os"
	"strconv"
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/repository"
	"github.com/stretchr/testify/require"
)

func TestConfiguredDefaultStepTimeout(t *testing.T) {
	for _, limits := range [][3]int64{{2, 0, 2}, {600, 0, 600}, {2, 7, 7}} {
		t.Run(strconv.FormatInt(limits[2], 10), func(t *testing.T) {
			r, repo, trace := podmanFixture(t, `
case "$3" in
run) cat >/dev/null; printf '%s\n' "$@" > "$PODMAN_TRACE";;
esac
`)
			settings, err := config.Load()
			require.NoError(t, err)
			settings.Limits.Execution.DefaultTimeoutSeconds = limits[0]
			repo = repository.NewConfigured(repo.DB, settings)
			r.repo = repo
			settings.Limits.Execution.DefaultTimeoutSeconds = 1
			t.Setenv("DURPDEPLOY_LIMITS_EXECUTION_DEFAULT_TIMEOUT_SECONDS", "1")
			writer := attemptLogWriter(t, r, repo, 1)
			err = r.runStepAttempt(t.Context(), localStepAttempt{
				deploymentID: 1, attempt: 1, logWriter: writer,
				step: deploymentStep{
					Name: "default timeout", ScriptBody: "true",
					ContainerImage: "alpine:3.20", TimeoutSeconds: limits[1],
				},
			})
			require.NoError(t, err)
			args, err := os.ReadFile(trace)
			require.NoError(t, err)
			require.Contains(
				t,
				string(args),
				"--timeout="+strconv.FormatInt(limits[2], 10)+"\n",
			)
		})
	}
}

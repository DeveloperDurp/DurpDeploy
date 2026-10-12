//go:build linux

package runner

import (
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"github.com/stretchr/testify/require"
)

func TestLocalCleanupDiagnosticSurvivesRejectedProcessOutput(t *testing.T) {
	// Given: a real attempt overflows pending output and removal fails.
	r, repo, _ := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) printf 'buffered-secret'; head -c 1048577 /dev/zero | tr '\000' X; exec sleep 30;;
rm) printf 'cleanup failed\n'; exit 7;;
esac
`)
	writer := attemptLogWriter(t, r, repo, 1)
	writer.scrubber = NewScrubber(
		[]string{"diagnostic-secret", "buffered-secret"},
	)
	// When: cleanup emits an internal diagnostic after process output is rejected.
	err := r.runStepAttempt(t.Context(), localStepAttempt{
		deploymentID: 1,
		attempt:      1,
		logWriter:    writer,
		step: deploymentStep{
			Name:           "diagnostic-secret",
			ContainerImage: "example.com/worker:1",
		},
	})
	// Then: failure state and a scrubbed cleanup diagnostic persist without pending plaintext.
	require.True(t, errors.Is(err, errPendingLogLimit))
	require.True(t, errors.Is(err, errContainerCleanup))
	require.True(t, writer.outputFailed)
	require.Zero(t, writer.buf.Len())
	logs, err := repo.Queries.ListDeploymentLogsByDeployment(t.Context(), 1)
	require.NoError(t, err)
	var diagnostic, failure bool
	for _, log := range logs {
		require.NotContains(t, log.Line, "buffered-secret")
		require.NotContains(t, log.Line, "diagnostic-secret")
		require.NotContains(t, log.Line, "XXXXX")
		diagnostic = diagnostic ||
			strings.Contains(log.Line, "container cleanup unconfirmed")
		failure = failure || log.StepState.String == "failed"
	}
	require.True(t, diagnostic)
	require.True(t, failure)
}

func TestLocalCleanupFailureRetainsOverflowStatusAndTailOrdering(t *testing.T) {
	for _, mode := range []string{"overflow", "tail"} {
		t.Run(mode, func(t *testing.T) {
			// Given: the real runner uses a runtime that cannot confirm container removal.
			output := "printf safe-tail"
			if mode == "overflow" {
				output = "printf buffered-secret; head -c 1048577 /dev/zero | tr '\\000' X; exec sleep 30"
			}
			r, repo, _ := podmanFixture(t, `
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps) ;;
run) `+output+`;;
rm) printf 'cleanup failed\n'; exit 7;;
esac
`)
			project, err := repo.Queries.CreateProject(
				t.Context(),
				db.CreateProjectParams{Name: "cleanup"},
			)
			require.NoError(t, err)
			env, err := repo.Queries.CreateEnvironment(
				t.Context(),
				db.CreateEnvironmentParams{Name: "cleanup"},
			)
			require.NoError(t, err)
			release, err := repo.Queries.CreateRelease(
				t.Context(),
				db.CreateReleaseParams{
					ProjectID: project.ID,
					Version:   "1",
					StepsJson: `[{"name":"local","container_image":"example.com/worker:1","script_body":"true"}]`,
				},
			)
			require.NoError(t, err)
			created, err := repo.CreateDeployment(
				t.Context(),
				db.CreateDeploymentParams{
					ReleaseID:     release.ID,
					EnvironmentID: env.ID,
					Status:        "pending",
				},
			)
			require.NoError(t, err)
			// When: execution finishes after overflow or a final partial line.
			r.Run(t.Context(), created.Deployment.ID, release.ID, env.ID)
			// Then: cleanup remains unconfirmed, and normal output precedes its diagnostic.
			stored, err := repo.Queries.GetDeployment(
				t.Context(),
				created.Deployment.ID,
			)
			require.NoError(t, err)
			require.Equal(t, "cleanup_unconfirmed", stored.Status)
			require.True(t, stored.FinishedAt.Valid)
			logs, err := repo.Queries.ListDeploymentLogsByDeployment(
				t.Context(),
				created.Deployment.ID,
			)
			require.NoError(t, err)
			tail, diagnostic := int64(-1), int64(-1)
			for _, log := range logs {
				require.NotContains(t, log.Line, "buffered-secret")
				require.NotContains(t, log.Line, "XXXXX")
				if log.Line == "safe-tail" {
					tail = log.ID
				}
				if strings.Contains(log.Line, "container cleanup unconfirmed") {
					diagnostic = log.ID
				}
			}
			require.GreaterOrEqual(t, diagnostic, int64(0))
			if mode == "tail" {
				require.GreaterOrEqual(t, tail, int64(0))
				require.Less(t, tail, diagnostic)
			}
		})
	}
}

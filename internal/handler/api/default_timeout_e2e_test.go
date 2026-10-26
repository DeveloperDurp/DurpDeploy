//go:build e2e

package api_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newDefaultTimeoutFixture(t *testing.T, seconds int64) *artifactE2E {
	t.Helper()
	settings := config.Defaults()
	settings.Container.Runtime = os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	settings.Container.Namespace = "default-timeout-" + uuid.NewString()
	settings.Limits.Execution.DefaultTimeoutSeconds = seconds
	f := newArtifactPolicyFixture(t, settings)
	f.environment = seedEnv(t, f.h.repo)
	settings.Limits.Execution.DefaultTimeoutSeconds = 1
	t.Setenv("DURPDEPLOY_LIMITS_EXECUTION_DEFAULT_TIMEOUT_SECONDS", "1")
	return f
}

func TestConfiguredDefaultTimeoutAPIE2E(t *testing.T) {
	for _, tc := range []struct {
		seconds, explicit int64
		pause             int
		status            string
	}{
		{3, 0, 4, "failed"}, {6, 0, 4, "succeeded"},
		{3, 12, 4, "succeeded"}, {600, 0, 0, "succeeded"},
	} {
		t.Run(
			fmt.Sprintf("default=%d/explicit=%d", tc.seconds, tc.explicit),
			func(t *testing.T) {
				f := newDefaultTimeoutFixture(t, tc.seconds)
				if stagingRuntime(
					t,
					"images",
					"--quiet",
					configuredPIDImage,
				) == "" {
					stagingRuntime(t, "pull", "--quiet", configuredPIDImage)
				}
				f.api(t, "POST", f.base()+"/steps", map[string]any{
					"name": "Default deadline", "interpreter": "python3",
					"container_image": configuredPIDImage, "timeout_seconds": tc.explicit,
					"script_body": fmt.Sprintf(
						"import time\nprint('deadline-start', flush=True)\ntime.sleep(%d)\nprint('deadline-complete', flush=True)",
						tc.pause,
					),
				}, 201)
				release := verificationRelease(t, f, "default-timeout")
				deployment := verificationDeploy(t, f, release)
				waitVerificationStatus(t, f, deployment.ID, tc.status)
				path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
				logs := string(f.api(t, "GET", path+"/logs", nil, 200))
				if tc.status == "failed" {
					require.Contains(t, logs, "timed out after 3s")
					require.NotContains(t, logs, "deadline-complete")
				} else {
					require.Contains(t, logs, "deadline-complete")
				}
				var policy config.Limits
				decodeLifecycleTest(
					t,
					f.api(t, "GET", "/api/v1/admin/resource-config", nil, 200),
					&policy,
				)
				require.Equal(
					t,
					tc.seconds,
					policy.Execution.DefaultTimeoutSeconds,
				)
				stored, err := f.h.repo.Queries.ListDeploymentSteps(
					t.Context(),
					deployment.ID,
				)
				require.NoError(t, err)
				require.Len(t, stored, 1)
				require.Equal(t, tc.explicit, stored[0].TimeoutSeconds)
				filter := "--filter=label=io.durpdeploy.namespace=" + os.Getenv(
					"DURPDEPLOY_CONTAINER_RUNTIME",
				) + ":" + os.Getenv(
					"DURPDEPLOY_CONTAINER_NAMESPACE",
				)
				require.Empty(
					t,
					strings.TrimSpace(
						stagingRuntime(t, "ps", "--all", "--quiet", filter),
					),
				)
				require.Empty(
					t,
					strings.TrimSpace(
						stagingRuntime(t, "volume", "ls", "--quiet", filter),
					),
				)
			},
		)
	}
}

func TestConfiguredDefaultTimeoutRemoteAPIE2E(t *testing.T) {
	f := newDefaultTimeoutFixture(t, 600)
	seedIncompatiblePolicyAgent(t, f)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Unsupported default deadline", "execution_target": "agent",
		"agent_execution_mode": "host", "script_body": "echo must-not-run",
	}, 201)
	release := verificationRelease(t, f, "remote-default-timeout")
	deployment := verificationDeploy(t, f, release)
	waitVerificationStatus(t, f, deployment.ID, "failed")
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
			nil,
			200,
		),
	)
	require.Contains(
		t,
		logs,
		"resource policy is incompatible",
	)
	var issued int
	require.NoError(
		t,
		f.h.repo.DB.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM remote_step_runs WHERE deployment_id=?", deployment.ID).
			Scan(&issued),
	)
	require.Zero(t, issued)
}

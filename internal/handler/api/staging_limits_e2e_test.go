//go:build e2e

package api_test

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/config"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newStagingLimitsFixture(
	t *testing.T,
	limits config.StagingLimits,
) *artifactE2E {
	t.Helper()
	s := config.Defaults()
	s.Container.Runtime = os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME")
	s.Container.Namespace = "staging-policy-" + uuid.NewString()
	s.Limits.Staging = limits
	f := newArtifactPolicyFixture(t, s)
	f.client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse }
	f.api(t, "DELETE", f.base()+"/package-repository", nil, 204)
	f.environment = seedEnv(t, f.h.repo)
	s.Limits.Staging.PayloadBytes = 1
	t.Setenv("DURPDEPLOY_LIMITS_STAGING_PAYLOAD_BYTES", "1")
	t.Setenv("DURPDEPLOY_LIMITS_STAGING_ENTRIES", "1")
	return f
}

func TestConfiguredStagingCaptureAPIWebE2E(t *testing.T) {
	for _, tc := range []struct {
		payload, entries, archive int64
		accepted                  bool
	}{{23, 3, 0, false}, {24, 2, 0, false}, {24, 3, 0, true}, {24, 3, 4095, false}, {24, 3, 4096, true}} {
		t.Run(fmt.Sprint(tc), func(t *testing.T) {
			limits := config.Defaults().Limits.Staging
			limits.PayloadBytes, limits.Entries = tc.payload, tc.entries
			if tc.archive != 0 {
				limits.ArchiveBytes = tc.archive
			}
			f := newStagingLimitsFixture(t, limits)
			f.api(t, "POST", f.base()+"/steps", map[string]any{
				"name": "Bounded context", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
				"approval_artifact_path": "plan", "approval_review_path": "review",
				"approval_review_format": "summary",
				"script_body": `set -eu
printf plan > "$DURPDEPLOY_STAGE_DIR/plan"
printf '{"create":0}' > "$DURPDEPLOY_STAGE_DIR/review"
printf 12345678 > "$DURPDEPLOY_STAGE_DIR/extra"`,
			}, 201)
			f.api(t, "POST", f.base()+"/steps", map[string]any{
				"name": "After context", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
				"script_body": "echo must-not-run",
			}, 201)
			release := verificationRelease(t, f, "staging-policy")
			d := verificationDeploy(t, f, release)
			want := "failed"
			if tc.accepted {
				want = "awaiting_artifact_approval"
			}
			waitVerificationStatus(t, f, d.ID, want)
			path := fmt.Sprintf("/api/v1/deployments/%d", d.ID)
			logs := string(f.api(t, "GET", path+"/logs", nil, 200))
			require.NotContains(t, logs, "must-not-run")
			if tc.accepted {
				f.api(t, "POST", path+"/cancel", nil, 200)
				waitVerificationStatus(t, f, d.ID, "cancelled")
			}
			assertStagingPolicyCleanup(t, f)
			f.web(
				t,
				"POST",
				fmt.Sprintf("/projects/%d/deploy", f.project.ID),
				url.Values{
					"release_id": {
						fmt.Sprint(release.ID),
					}, "environment_id": {fmt.Sprint(f.environment.ID)},
				},
				303,
			)
			d = latestNotesDeployment(t, f)
			waitVerificationStatus(t, f, d.ID, want)
			if tc.accepted {
				f.api(
					t,
					"POST",
					fmt.Sprintf("/api/v1/deployments/%d/cancel", d.ID),
					nil,
					200,
				)
				waitVerificationStatus(t, f, d.ID, "cancelled")
			}
			assertStagingPolicyCleanup(t, f)
		})
	}
}

func assertStagingPolicyCleanup(t *testing.T, f *artifactE2E) {
	t.Helper()
	scope := f.h.repo.StartupLookup
	namespace, _ := scope("DURPDEPLOY_CONTAINER_NAMESPACE")
	runtime, _ := scope("DURPDEPLOY_CONTAINER_RUNTIME")
	filter := "--filter=label=io.durpdeploy.namespace=" + runtime + ":" + namespace
	require.Empty(t, stagingRuntime(t, "ps", "--all", "--quiet", filter))
	require.Empty(t, stagingRuntime(t, "volume", "ls", "--quiet", filter))
}

func TestConfiguredStagingCapacityAPIE2E(t *testing.T) {
	for _, raised := range []bool{false, true} {
		t.Run(fmt.Sprint(raised), func(t *testing.T) {
			limits := config.Defaults().Limits.Staging
			limits.PayloadBytes, limits.Entries = 65536, 8
			if raised {
				limits.PayloadBytes = (512 << 20) + 4096
				limits.Entries = 20001
			}
			f := newStagingLimitsFixture(t, limits)
			capacity := limits.PayloadBytes + limits.Entries*int64(
				os.Getpagesize(),
			)
			f.api(t, "POST", f.base()+"/steps", map[string]any{
				"name": "Capacity probe", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
				"script_body": fmt.Sprintf(`set -eu
stat -f -c '%%S %%b %%c' "$DURPDEPLOY_STAGE_DIR" | awk '{ if ($1 * $2 != %d || $3 != %d) exit 1 }'
printf staged > "$DURPDEPLOY_STAGE_DIR/file"
echo configured-stage-ready
while [ ! -e /tmp/finish ]; do sleep 0.1; done`, capacity, 2*(limits.Entries+1)),
			}, 201)
			d := verificationDeploy(t, f, verificationRelease(t, f, "capacity"))
			path := fmt.Sprintf("/api/v1/deployments/%d", d.ID)
			waitVerificationLog(t, f, path, "configured-stage-ready")
			namespace, _ := f.h.repo.StartupLookup(
				"DURPDEPLOY_CONTAINER_NAMESPACE",
			)
			filter := "--filter=label=io.durpdeploy.namespace=" + os.Getenv(
				"DURPDEPLOY_CONTAINER_RUNTIME",
			) + ":" + namespace
			ids := strings.Fields(stagingRuntime(t, "ps", "--quiet", filter))
			require.Len(t, ids, 2)
			workload := ""
			for _, id := range ids {
				name := stagingRuntime(t, "inspect", "--format={{.Name}}", id)
				if strings.Contains(name, "-keeper") {
					require.Equal(
						t,
						fmt.Sprint(
							capacity+config.Defaults().Limits.Container.HelperMemoryOverheadBytes,
						),
						stagingRuntime(
							t,
							"inspect",
							"--format={{.HostConfig.Memory}}",
							id,
						),
					)
				} else {
					workload = id
				}
			}
			require.NotEmpty(t, workload)
			stagingRuntime(t, "exec", workload, "touch", "/tmp/finish")
			waitVerificationStatus(t, f, d.ID, "succeeded")
			var captured config.Limits
			decodeLifecycleTest(
				t,
				f.api(t, "GET", "/api/v1/admin/resource-config", nil, 200),
				&captured,
			)
			require.Equal(t, limits, captured.Staging)
			assertStagingPolicyCleanup(t, f)
		})
	}
}

func TestConfiguredStagingRemoteAPIE2E(t *testing.T) {
	s := config.Defaults()
	s.Container.Enabled = "false"
	s.Limits.Staging.Entries = 8
	f := newArtifactPolicyFixture(t, s)
	f.api(t, "DELETE", f.base()+"/package-repository", nil, 204)
	f.environment = seedEnv(t, f.h.repo)
	seedIncompatiblePolicyAgent(t, f)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Unsupported staging policy", "execution_target": "agent",
		"agent_execution_mode": "host", "script_body": "echo must-not-run",
	}, 201)
	d := verificationDeploy(t, f, verificationRelease(t, f, "remote-staging"))
	waitVerificationStatus(t, f, d.ID, "failed")
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", d.ID),
			nil,
			200,
		),
	)
	require.Contains(
		t,
		logs,
		"resource policy is incompatible",
	)
}

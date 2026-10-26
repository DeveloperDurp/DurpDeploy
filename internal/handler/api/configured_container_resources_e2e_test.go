//go:build e2e

package api_test

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/config"
	"durpdeploy/internal/staging"
	"github.com/stretchr/testify/require"
)

func TestConfiguredContainerResourcesAPIE2E(t *testing.T) {
	for _, raised := range []bool{false, true} {
		for _, approval := range []bool{false, true} {
			t.Run(
				fmt.Sprintf("raised=%t/approval=%t", raised, approval),
				func(t *testing.T) {
					testConfiguredContainerResources(t, raised, approval)
				},
			)
		}
	}
}

func testConfiguredContainerResources(t *testing.T, raised, approval bool) {
	t.Helper()
	limits := config.Defaults().Limits.Container
	limits.TmpfsBytes, limits.HelperCPUs = 1<<20, 0.5
	limits.HelperMemoryOverheadBytes = 8 << 20
	if raised {
		limits.TmpfsBytes, limits.HelperCPUs = 128<<20, 1.5
		limits.HelperMemoryOverheadBytes = 256 << 20
	}
	f := newConfiguredArtifactE2E(t, &limits)
	if stagingRuntime(t, "images", "--quiet", configuredPIDImage) == "" {
		stagingRuntime(t, "pull", "--quiet", configuredPIDImage)
	}
	wantTmpfs := limits.TmpfsBytes
	step := map[string]any{
		"name": "Container resource probe", "interpreter": "python3",
		"container_image": configuredPIDImage,
	}
	if approval {
		step["approval_artifact_path"] = "plan"
		step["approval_review_path"] = "review"
		step["approval_review_format"] = "summary"
		wantTmpfs += config.Defaults().Limits.Artifact.DownloadBytes
	}
	step["script_body"] = fmt.Sprintf(containerFilesystemProbe, wantTmpfs)
	f.api(t, "POST", f.base()+"/steps", step, 201)
	if approval {
		f.api(t, "POST", f.base()+"/steps", map[string]any{
			"name": "Apply after approval", "script_body": "print('must-not-run')",
			"interpreter": "python3", "container_image": configuredPIDImage,
		}, 201)
	}
	release := verificationRelease(t, f, "container-resources")
	deployment := verificationDeploy(t, f, release)
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	waitVerificationLog(t, f, path, "filesystem-budget-confirmed")
	filter := "--filter=label=io.durpdeploy.namespace=" +
		os.Getenv("DURPDEPLOY_CONTAINER_RUNTIME") + ":" +
		os.Getenv("DURPDEPLOY_CONTAINER_NAMESPACE")
	ids := strings.Fields(stagingRuntime(t, "ps", "--quiet", filter))
	require.Len(t, ids, 2, "workload and staging helper must remain available")
	inspectConfiguredHelperResources(t, ids, limits)
	f.api(t, "POST", path+"/cancel", nil, 200)
	waitVerificationStatus(t, f, deployment.ID, "cancelled")
	require.Empty(t, stagingRuntime(t, "ps", "--all", "--quiet", filter))
	require.Empty(t, stagingRuntime(t, "volume", "ls", "--quiet", filter))
}

func inspectConfiguredHelperResources(
	t *testing.T,
	ids []string,
	limits config.ContainerLimits,
) {
	t.Helper()
	for _, id := range ids {
		name := stagingRuntime(t, "inspect", "--format={{.Name}}", id)
		if !strings.Contains(name, "-keeper") {
			continue
		}
		values := strings.Fields(stagingRuntime(t, "exec", id, "cat",
			"/sys/fs/cgroup/cpu.max", "/sys/fs/cgroup/memory.max"))
		require.Len(t, values, 3)
		quota, err := strconv.ParseFloat(values[0], 64)
		require.NoError(t, err)
		period, err := strconv.ParseFloat(values[1], 64)
		require.NoError(t, err)
		require.Equal(t, limits.HelperCPUs, quota/period)
		memory, err := strconv.ParseInt(values[2], 10, 64)
		require.NoError(t, err)
		capacity := staging.MaxBytes + int64(
			staging.MaxFiles,
		)*int64(
			os.Getpagesize(),
		)
		require.Equal(t, capacity+limits.HelperMemoryOverheadBytes, memory)
		return
	}
	t.Fatal("staging helper was not found")
}

const containerFilesystemProbe = `import errno, os, pathlib, time
expected = %d
filesystem = os.statvfs('/tmp')
assert filesystem.f_blocks * filesystem.f_frsize == expected
written = 0
try:
    with open('/tmp/resource-probe', 'wb', buffering=0) as output:
        while written <= expected:
            written += output.write(b'x' * 65536)
except OSError as error:
    assert error.errno == errno.ENOSPC, error
else:
    raise AssertionError('temporary filesystem exceeded its budget')
finally:
    pathlib.Path('/tmp/resource-probe').unlink(missing_ok=True)
assert expected - 65536 <= written <= expected, written
print('filesystem-budget-confirmed', expected, written, flush=True)
while True:
    time.sleep(0.05)
`

func TestConfiguredContainerResourcesRemoteRejectionAPIE2E(t *testing.T) {
	for _, field := range []string{"tmpfs", "helper CPUs", "helper memory"} {
		t.Run(field, func(t *testing.T) {
			limits := config.Defaults().Limits.Container
			switch field {
			case "tmpfs":
				limits.TmpfsBytes = 1 << 20
			case "helper CPUs":
				limits.HelperCPUs = 0.5
			case "helper memory":
				limits.HelperMemoryOverheadBytes = 8 << 20
			}
			testConfiguredRemoteContainerRejection(
				t,
				limits,
			)
		})
	}
}

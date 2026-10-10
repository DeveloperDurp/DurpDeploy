package testutil

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
)

func TestGoTestRunnerRemovesContainerAfterTimeout(t *testing.T) {
	testcontainers.SkipIfProviderIsNotHealthy(t)
	// Given: a subprocess fixture that loses t.Cleanup on Go's timeout panic.
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	idPath := filepath.Join(t.TempDir(), "container-id")
	root, err := filepath.Abs("../..")
	require.NoError(t, err)
	api, err := testcontainers.NewDockerClientWithOpts(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, api.Close()) })
	t.Cleanup(func() {
		id, err := os.ReadFile(idPath)
		if os.IsNotExist(err) {
			return
		}
		require.NoError(t, err)
		cleanupCtx, cleanupCancel := context.WithTimeout(
			context.Background(), time.Minute)
		defer cleanupCancel()
		_, err = api.ContainerRemove(cleanupCtx, string(id),
			client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		if !errdefs.IsNotFound(err) {
			require.NoError(t, err)
		}
	})
	command := exec.CommandContext(ctx, "bash", "scripts/go_test.sh",
		"-count=1", "-timeout=30s", "-run=^TestGoTestRunnerTimeoutFixture$",
		"./internal/testutil")
	command.Dir = root
	command.Env = append(os.Environ(),
		"DURPDEPLOY_TEST_TIMEOUT_CONTAINER_ID="+idPath,
		"TESTCONTAINERS_RYUK_DISABLED=true")

	// When: the test process times out with the automatic reaper disabled.
	output, err := command.CombinedOutput()
	require.Error(t, err, "expected timeout, output: %s", output)
	require.Contains(t, string(output), "test timed out after 30s")

	// Then: the runner has removed that process's container and volume.
	id, err := os.ReadFile(idPath)
	require.NoError(t, err, "fixture did not create a container: %s", output)
	_, err = api.ContainerInspect(ctx, string(id),
		client.ContainerInspectOptions{})
	require.True(t, errdefs.IsNotFound(err),
		"container survived test timeout: %v; output: %s", err, output)
}

func TestGoTestRunnerTimeoutFixture(t *testing.T) {
	idPath := os.Getenv("DURPDEPLOY_TEST_TIMEOUT_CONTAINER_ID")
	if idPath == "" {
		t.Skip("subprocess-only timeout fixture")
	}
	// Given: an actual database container registered for normal cleanup.
	container, err := testcontainers.GenericContainer(t.Context(),
		testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				Image: "postgres:16-alpine",
				Env: map[string]string{
					"POSTGRES_PASSWORD": "fixture-only",
				},
			},
			Started: true,
		})
	CleanupContainer(t, container)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(idPath,
		[]byte(container.GetContainerID()), 0o600))
	// When: the Go test timeout kills the process before cleanup can execute.
	// Then: the parent regression test checks the runner's cleanup.
	<-t.Context().Done()
}

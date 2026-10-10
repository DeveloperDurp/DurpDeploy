package testutil

import (
	"context"
	"testing"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

func TestCleanupContainerRemovesFailedStartup(t *testing.T) {
	// Given: a database that starts but fails its readiness check.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	var container *postgres.PostgresContainer
	t.Cleanup(func() { CleanupContainer(t, container) })

	// When: setup skips the test after a startup error.
	t.Run("startup failure", func(t *testing.T) {
		var err error
		container, err = postgres.Run(ctx, "postgres:16-alpine",
			postgres.WithPassword("fixture-only"),
			testcontainers.WithWaitStrategy(wait.ForLog(
				"deliberately absent readiness marker",
			).WithStartupTimeout(time.Second)),
		)
		CleanupContainer(t, container)
		require.NotNil(t, container)
		require.Error(t, err)
		if err != nil {
			t.Skip("expected startup failure")
		}
	})

	// Then: the partially started container is removed by the subtest.
	api, err := testcontainers.NewDockerClientWithOpts(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, api.Close()) })
	_, err = api.ContainerInspect(ctx, container.GetContainerID(),
		client.ContainerInspectOptions{})
	require.True(t, errdefs.IsNotFound(err),
		"container survived failed setup: %v", err)
}

func TestCleanupContainerRemovesContainerAfterContextCancellation(
	t *testing.T,
) {
	// Given: a started database and a context canceled before teardown.
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithPassword("fixture-only"),
		postgres.BasicWaitStrategies())
	CleanupContainer(t, container)
	require.NoError(t, err)

	// When: test-scoped cleanup runs after the startup context is canceled.
	t.Run("canceled context", func(t *testing.T) {
		CleanupContainer(t, container)
		cancel()
	})

	// Then: cleanup uses an independent context and removes the database.
	api, err := testcontainers.NewDockerClientWithOpts(t.Context())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, api.Close()) })
	_, err = api.ContainerInspect(t.Context(), container.GetContainerID(),
		client.ContainerInspectOptions{})
	require.True(t, errdefs.IsNotFound(err),
		"container survived canceled context: %v", err)
}

func TestCleanupContainerAcceptsNilHandles(t *testing.T) {
	// Given: failures before a container handle can be returned.
	var container *postgres.PostgresContainer
	// When: cleanup is registered before checking the setup error.
	CleanupContainer(t, nil)
	CleanupContainer(t, container)
	// Then: cleanup completes without dereferencing either nil handle.
}

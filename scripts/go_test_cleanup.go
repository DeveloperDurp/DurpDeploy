//go:build ignore

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/containerd/errdefs"
	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	// Use the fixtures' SDK for both endpoint discovery and removal. This
	// supports Docker and Podman without requiring either provider's CLI.
	api, err := testcontainers.NewDockerClientWithOpts(ctx)
	if err != nil {
		fmt.Fprintln(os.Stderr, "test container provider unavailable:", err)
		os.Exit(2)
	}
	err = cleanup(context.Background(), api, os.Args[1:])
	err = errors.Join(err, api.Close())
	if err != nil {
		fmt.Fprintln(os.Stderr, "test container cleanup:", err)
		os.Exit(1)
	}
}

func cleanup(ctx context.Context, api *testcontainers.DockerClient,
	sessions []string,
) error {
	if len(sessions) == 1 && sessions[0] == "--host" {
		_, err := fmt.Println(api.DaemonHost())
		return err
	}
	// Snapshot the budget before an operation can consume its deadline.
	timeout := cleanupTimeout(ctx)
	ctx = context.WithoutCancel(ctx)
	failures := []error{cleanupRecoverySessions(ctx, api, sessions, timeout)}
	for _, session := range sessions {
		if !strings.HasPrefix(session, "durpdeploy-go-test.") {
			failures = append(
				failures,
				fmt.Errorf("invalid session %q", session),
			)
			continue
		}
		filters := make(client.Filters)
		filters.Add("label", "org.testcontainers=true",
			"org.testcontainers.sessionId="+session)
		callCtx, cancel := context.WithTimeout(ctx, timeout)
		containers, err := api.ContainerList(
			callCtx,
			client.ContainerListOptions{
				All: true, Filters: filters,
			},
		)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("list %s: %w", session, err))
			continue
		}
		for _, container := range containers.Items {
			if container.Labels["org.testcontainers"] != "true" ||
				container.Labels["org.testcontainers.sessionId"] != session {
				continue
			}
			if err := remove(ctx, api, container.ID, timeout); err != nil {
				failures = append(failures,
					fmt.Errorf("remove %s: %w", container.ID, err))
			}
		}
	}
	return errors.Join(failures...)
}

func cleanupTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return min(time.Minute, time.Until(deadline))
	}
	return time.Minute
}

func cleanupRecoverySessions(ctx context.Context,
	api *testcontainers.DockerClient, sessions []string, timeout time.Duration,
) error {
	var failures []error
	for _, session := range sessions {
		if !strings.HasPrefix(session, "durpdeploy-go-test.") {
			continue
		}
		failures = append(
			failures,
			cleanupRecoveryRuntime(ctx, api, session, timeout),
		)
	}
	return errors.Join(failures...)
}

// Recovery tests use this session-derived namespace for application-created
// containers and named staging volumes, which have no Testcontainers labels.
func cleanupRecoveryRuntime(
	ctx context.Context,
	api *testcontainers.DockerClient,
	session string,
	timeout time.Duration,
) error {
	var failures []error
	suffix := strings.ReplaceAll(session, ".", "-") + "-restore"
	for _, kind := range []string{"docker", "podman"} {
		namespace := kind + ":" + suffix
		failures = append(
			failures,
			cleanupRecoveryContainers(ctx, api, namespace, timeout),
			cleanupRecoveryVolumes(ctx, api, namespace, timeout),
		)
	}
	return errors.Join(failures...)
}

func cleanupRecoveryContainers(
	ctx context.Context,
	api *testcontainers.DockerClient,
	namespace string,
	timeout time.Duration,
) error {
	filters := make(client.Filters)
	filters.Add("label", "io.durpdeploy.namespace="+namespace)
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	containers, err := api.ContainerList(callCtx, client.ContainerListOptions{
		All: true, Filters: filters,
	})
	cancel()
	if err != nil {
		return err
	}
	var failures []error
	for _, resource := range containers.Items {
		if resource.Labels["io.durpdeploy.namespace"] != namespace {
			continue
		}
		if err := remove(ctx, api, resource.ID, timeout); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func cleanupRecoveryVolumes(ctx context.Context,
	api *testcontainers.DockerClient, namespace string, timeout time.Duration,
) error {
	filters := make(client.Filters)
	filters.Add("label", "io.durpdeploy.namespace="+namespace)
	callCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	volumes, err := api.VolumeList(
		callCtx,
		client.VolumeListOptions{Filters: filters},
	)
	cancel()
	if err != nil {
		return err
	}
	var failures []error
	for _, resource := range volumes.Items {
		if resource.Labels["io.durpdeploy.namespace"] != namespace {
			continue
		}
		callCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			timeout,
		)
		_, err := api.VolumeRemove(callCtx, resource.Name,
			client.VolumeRemoveOptions{Force: true})
		cancel()
		if err != nil && !errdefs.IsNotFound(err) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func remove(ctx context.Context, api *testcontainers.DockerClient,
	id string, timeout time.Duration,
) error {
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()
	_, err := api.ContainerRemove(ctx, id,
		client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
	if err == nil || errdefs.IsNotFound(err) {
		return nil
	}
	for {
		// Ryuk can finish deletion after our listing or be deleting it now.
		_, inspectErr := api.ContainerInspect(ctx, id,
			client.ContainerInspectOptions{})
		if errdefs.IsNotFound(inspectErr) {
			return nil
		}
		if inspectErr != nil || !errdefs.IsConflict(err) {
			return errors.Join(err, inspectErr)
		}
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
	}
}

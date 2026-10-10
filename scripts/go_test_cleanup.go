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
	err = cleanup(ctx, api, os.Args[1:])
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
	var failures []error
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
		containers, err := api.ContainerList(ctx, client.ContainerListOptions{
			All: true, Filters: filters,
		})
		if err != nil {
			failures = append(failures, fmt.Errorf("list %s: %w", session, err))
			continue
		}
		for _, container := range containers.Items {
			if container.Labels["org.testcontainers"] != "true" ||
				container.Labels["org.testcontainers.sessionId"] != session {
				continue
			}
			_, err := api.ContainerRemove(ctx, container.ID,
				client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
			if err != nil && !errdefs.IsNotFound(err) {
				// Ryuk may have removed the container after our listing.
				_, inspectErr := api.ContainerInspect(ctx, container.ID,
					client.ContainerInspectOptions{})
				if !errdefs.IsNotFound(inspectErr) {
					failures = append(failures,
						fmt.Errorf("remove %s: %w", container.ID, err))
				}
			}
		}
	}
	return errors.Join(failures...)
}

//go:build e2e && artifactstress && linux

package api_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func (p *artifactProcessFixture) runtime(t *testing.T, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(
		context.WithoutCancel(t.Context()),
		30*time.Second,
	)
	defer cancel()
	output, err := p.runtimeResult(ctx, args...)
	if err != nil {
		t.Fatal(err)
	}
	return output
}

func (p *artifactProcessFixture) runtimeResult(
	ctx context.Context,
	args ...string,
) (string, error) {
	command := exec.CommandContext(
		ctx,
		p.runtimeBinary,
		append(append([]string{}, p.runtimeArgs...), args...)...)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		switch name {
		case "DOCKER_CONTEXT",
			"DOCKER_HOST",
			"CONTAINER_CONNECTION",
			"CONTAINER_HOST":
			continue
		}
		command.Env = append(command.Env, entry)
	}
	output, err := command.Output()
	if err != nil {
		if failure, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf(
				"runtime %s failed: %w: %s",
				args[0],
				err,
				failure.Stderr,
			)
		}
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

func (p *artifactProcessFixture) ownedContainers(t *testing.T) string {
	t.Helper()
	return p.runtime(
		t,
		"ps",
		"--all",
		"--quiet",
		"--filter=label=io.durpdeploy.namespace="+p.kind+":"+p.namespace,
	)
}

func (p *artifactProcessFixture) ownedVolumes(t *testing.T) string {
	t.Helper()
	return p.runtime(
		t,
		"volume",
		"ls",
		"--quiet",
		"--filter=label=io.durpdeploy.artifact=true",
		"--filter=label=io.durpdeploy.namespace="+p.kind+":"+p.namespace,
	)
}

func (p *artifactProcessFixture) unrelatedResources(
	t *testing.T,
) (string, string) {
	t.Helper()
	container, volume := p.namespace+"-other", p.namespace+"-other-volume"
	label := "--label=io.durpdeploy.namespace=" + p.kind + ":" + p.namespace + "-other"
	t.Cleanup(func() { p.cleanupRuntime(t, container, volume) })
	p.runtime(
		t,
		"volume",
		"create",
		"--label=io.durpdeploy.artifact=true",
		label,
		volume,
	)
	p.runtime(
		t,
		"run",
		"--detach",
		"--pull=missing",
		"--name="+container,
		label,
		"--network=none",
		"--read-only",
		"--cap-drop=ALL",
		"--user=65534:65534",
		"--memory=32m",
		"--pids-limit=8",
		"--entrypoint=/bin/sh",
		"docker.io/library/bash:5.2",
		"-c",
		"exec sleep 600",
	)
	return container, volume
}

func (p *artifactProcessFixture) gateTransport(t *testing.T) {
	t.Helper()
	directory := filepath.Join(p.root, "runtime-bin")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(
		filepath.Join(p.root, "stage-release"),
		0600,
	); err != nil {
		t.Fatal(err)
	}
	// Only the tar-transfer command waits. Every runtime operation still uses
	// the real daemon, including volume creation and the live staging keeper.
	script := fmt.Sprintf(`#!/usr/bin/env bash
set -eu
case " $* " in
  *" exec --interactive "*" tar -xf - -C /artifacts "*)
    {
      head -c 65536
      printf ready > "$DURPDEPLOY_ARTIFACT_TEST_GATE"
      read -r ignored < "$DURPDEPLOY_ARTIFACT_TEST_RELEASE"
      cat
    } | %q "$@"
    exit "$?"
    ;;
esac
exec %q "$@"
`, p.runtimeBinary, p.runtimeBinary)
	if err := os.WriteFile(
		filepath.Join(directory, p.kind),
		[]byte(script),
		0700,
	); err != nil {
		t.Fatal(err)
	}
}

func (p *artifactProcessFixture) cleanupRuntime(
	t *testing.T,
	container, volume string,
) {
	t.Helper()
	// Each command has its own deadline and errors do not stop later removals.
	command := func(args ...string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		output, err := p.runtimeResult(ctx, args...)
		if err != nil {
			message := strings.ToLower(err.Error())
			if !strings.Contains(message, "no such container") &&
				!strings.Contains(message, "no such volume") &&
				!strings.Contains(message, "no container with") &&
				!strings.Contains(message, "no volume with") {
				t.Error(err)
			}
		}
		return output
	}
	filter := "--filter=label=io.durpdeploy.namespace=" + p.kind + ":" + p.namespace
	for _, id := range strings.Fields(command("ps", "--all", "--quiet", filter)) {
		command("rm", "--force", id)
	}
	for _, id := range strings.Fields(command("volume", "ls", "--quiet", "--filter=label=io.durpdeploy.artifact=true", filter)) {
		command("volume", "rm", id)
	}
	command("rm", "--force", container)
	command("volume", "rm", volume)
}

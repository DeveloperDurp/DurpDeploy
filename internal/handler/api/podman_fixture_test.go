package api_test

import (
	"os"
	"path/filepath"
	"testing"
)

func fakePodman(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "podman")
	cli := `#!/bin/sh
case "$4" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps|rm) ;;
run) exec bash -c "$(cat)";;
esac
`
	if err := os.WriteFile(
		binary,
		[]byte(cli),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv(
		"DURPDEPLOY_PODMAN_URL",
		"ssh://executor@example.invalid/run/user/1234/podman/podman.sock",
	)
	t.Setenv("DURPDEPLOY_PODMAN_NAMESPACE", "api-tests")
	return binary
}

func fakeDocker(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	cli := `#!/bin/sh
case "$2" in
info) printf '["name=rootless"]';;
ps|rm) ;;
run)
  timeout=
  for arg do
    case "$arg" in
      --stop-timeout=*) timeout=1 ;;
      --timeout=*|--image-volume=*|--http-proxy=*|--rm) exit 64 ;;
    esac
  done
  test "$timeout" = 1
  exec bash -c "$(cat)";;
esac
`
	if err := os.WriteFile(binary, []byte(cli), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "docker")
	t.Setenv("DURPDEPLOY_CONTAINER_URL",
		"ssh://executor@example.invalid/run/user/1234/docker.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "api-tests")
	return binary
}

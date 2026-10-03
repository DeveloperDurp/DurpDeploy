package api_test

import (
	"os"
	"path/filepath"
	"testing"
)

func fakePodman(t *testing.T) string {
	t.Helper()
	t.Setenv("DURPDEPLOY_CONTAINER_RUNTIME", "podman")
	dir := t.TempDir()
	binary := filepath.Join(dir, "podman")
	cli := `#!/bin/sh
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}';;
ps|rm) ;;
inspect) printf 'true\n';;
run)
  for arg do case "$arg" in --detach) exit 0;; esac; done
  exec bash -c "$(cat)";;
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
		"DURPDEPLOY_CONTAINER_URL",
		"unix:///run/podman/podman.sock",
	)
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "api-tests")
	return binary
}

func fakeDocker(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "docker")
	cli := `#!/bin/sh
case "$2" in
info) printf '["name=rootless"]';;
image) printf 'null';;
ps|rm) ;;
inspect) printf 'true\n';;
run)
  timeout=
  for arg do
    case "$arg" in
      --detach) exit 0 ;;
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
		"unix:///var/run/docker.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "api-tests")
	return binary
}

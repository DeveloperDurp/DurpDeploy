package handler_test

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestPodman(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	binary := filepath.Join(dir, "podman")
	cli := `#!/bin/sh
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}' ;;
ps|rm) ;;
run) exec /bin/sh -s ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(
		binary,
		[]byte(cli),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DURPDEPLOY_CONTAINER_URL",
		"unix:///run/podman/podman.sock")
	t.Setenv("DURPDEPLOY_CONTAINER_NAMESPACE", "handler-tests")
	return binary
}

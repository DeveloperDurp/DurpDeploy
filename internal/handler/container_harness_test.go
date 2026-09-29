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
case "$4" in
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
	t.Setenv("DURPDEPLOY_PODMAN_URL",
		"ssh://executor@example.invalid/run/user/1234/podman/podman.sock")
	t.Setenv("DURPDEPLOY_PODMAN_NAMESPACE", "handler-tests")
	return binary
}

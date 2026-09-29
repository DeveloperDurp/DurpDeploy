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
case "$3" in
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

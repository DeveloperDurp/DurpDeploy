package handler_test

import (
	"os"
	"path/filepath"
	"testing"
)

func setupTestPodman(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	cli := `#!/bin/sh
case "$3" in
info) printf '{"host":{"security":{"rootless":true}}}' ;;
ps|rm) ;;
run) exec /bin/sh -s ;;
*) exit 1 ;;
esac
`
	if err := os.WriteFile(
		filepath.Join(dir, "podman"),
		[]byte(cli),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DURPDEPLOY_PODMAN_URL",
		"ssh://executor@example.invalid/run/user/1234/podman/podman.sock")
	t.Setenv("DURPDEPLOY_PODMAN_NAMESPACE", "handler-tests")
}

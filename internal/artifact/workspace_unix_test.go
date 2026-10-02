//go:build unix

package artifact

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspaceRejectsForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing directory ownership requires root")
	}
	// Given: an unprivileged user pre-created a private directory for a root server.
	base := t.TempDir()
	directory := filepath.Join(base, "durpdeploy-artifacts")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(directory, 65534, 65534); err != nil {
		t.Fatal(err)
	}

	// When: the server attempts startup.
	workspace, err := OpenWorkspace(base)
	if workspace != nil {
		defer workspace.Close()
	}

	// Then: it rejects the foreign-owned workspace rather than using its files.
	if err == nil {
		t.Fatal("foreign-owned workspace accepted")
	}
}

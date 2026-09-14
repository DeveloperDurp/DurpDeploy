//go:build linux

package runner

import (
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

func TestDeploymentRunner_CommandUsesScratchDirectoryWithoutMountIsolation(
	t *testing.T,
) {
	// Given
	runner := &DeploymentRunner{
		sandboxErr: nil,
	}
	tmpDir := t.TempDir()
	scriptPath := filepath.Join(tmpDir, "script.sh")

	// When
	cmd, err := runner.command(t.Context(), tmpDir, scriptPath)

	// Then
	if err != nil {
		t.Fatalf("construct command: %v", err)
	}
	if cmd.Dir != tmpDir {
		t.Fatalf("command directory = %q, want %q", cmd.Dir, tmpDir)
	}
	if cmd.SysProcAttr == nil {
		t.Fatal("command lacks process attributes")
	}
	if !cmd.SysProcAttr.Setpgid {
		t.Fatal("command lacks its own process group")
	}
	if cmd.SysProcAttr.Credential != nil {
		t.Fatalf("command switches credentials: %+v", cmd.SysProcAttr.Credential)
	}
	wantArgs := []string{"/bin/bash", scriptPath}
	if !slices.Equal(cmd.Args, wantArgs) {
		t.Fatalf("command args = %q, want %q", cmd.Args, wantArgs)
	}
	if cmd.SysProcAttr.Chroot != "" {
		t.Fatalf("command chroot = %q, want empty", cmd.SysProcAttr.Chroot)
	}
	if cmd.SysProcAttr.Cloneflags&syscall.CLONE_NEWNS != 0 {
		t.Fatalf(
			"clone flags = %#x, want no mount namespace",
			cmd.SysProcAttr.Cloneflags,
		)
	}
	if cmd.SysProcAttr.Unshareflags&syscall.CLONE_NEWNS != 0 {
		t.Fatalf(
			"unshare flags = %#x, want no mount namespace",
			cmd.SysProcAttr.Unshareflags,
		)
	}
}

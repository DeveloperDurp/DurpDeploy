//go:build linux

package runner

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestDeploymentRunner_CommandUsesScratchDirectoryWithoutMountIsolation(
	t *testing.T,
) {
	// Given
	runner := &DeploymentRunner{
		sandbox: &Sandbox{uid: 10002, gid: 10002, enabled: true},
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
	credential := cmd.SysProcAttr.Credential
	if credential == nil || credential.Uid != 10002 || credential.Gid != 10002 {
		t.Fatalf("command credential = %+v, want UID/GID 10002", credential)
	}
	if len(cmd.Args) < 6 || cmd.Args[0] != "setpriv" ||
		cmd.Args[1] != "--bounding-set=-all" ||
		cmd.Args[2] != "--inh-caps=-all" ||
		cmd.Args[3] != "--ambient-caps=-all" ||
		cmd.Args[4] != "--no-new-privs" || cmd.Args[5] != "--" {
		t.Fatalf("command capability wrapper = %q", cmd.Args)
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

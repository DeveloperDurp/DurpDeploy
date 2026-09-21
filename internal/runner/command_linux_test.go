//go:build linux

package runner

import (
	"errors"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
	cmd, err := runner.command(t.Context(), tmpDir, scriptPath, "bash")

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
		t.Fatalf(
			"command switches credentials: %+v",
			cmd.SysProcAttr.Credential,
		)
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Fatalf("find bash: %v", err)
	}
	wantArgs := []string{bash, scriptPath}
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

func TestDeploymentRunner_CommandReportsMissingInterpreter(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, err := (&DeploymentRunner{}).command(
		t.Context(), t.TempDir(), "script.py", "python3",
	)
	if err == nil || !errors.Is(err, exec.ErrNotFound) ||
		!strings.Contains(
			err.Error(),
			`interpreter "python3" is not installed`,
		) {
		t.Fatalf("command error = %v", err)
	}
}

func TestDeploymentRunner_CommandUsesSelectedInterpreter(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed")
	}
	scriptPath := filepath.Join(t.TempDir(), "script.py")
	cmd, err := (&DeploymentRunner{}).command(
		t.Context(), filepath.Dir(scriptPath), scriptPath, "python3",
	)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{python, scriptPath}; !slices.Equal(cmd.Args, want) {
		t.Fatalf("command args = %q, want %q", cmd.Args, want)
	}
}

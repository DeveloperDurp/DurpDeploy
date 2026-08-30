//go:build linux

package runner

import (
	"errors"
	"os"
	"os/exec"
	"os/user"
	"slices"
	"testing"
)

func TestSandbox_AppliesRunnerUidGid(t *testing.T) {
	// Given
	sandbox := &Sandbox{uid: 10002, gid: 10002, enabled: true}
	cmd := exec.Command("bash", "/script.sh")

	// When
	sandbox.applyCredential(cmd)

	// Then
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Credential == nil {
		t.Fatal("credential = nil, want runner credential")
	}
	credential := cmd.SysProcAttr.Credential
	if credential.Uid != 10002 || credential.Gid != 10002 {
		t.Fatalf("credential = %#v, want UID/GID 10002", credential)
	}
	if !slices.Equal(credential.Groups, []uint32{10002}) {
		t.Fatalf("groups = %v, want [10002]", credential.Groups)
	}
}

func TestSandbox_FailsClosed_WhenRunnerMissing(t *testing.T) {
	// Given
	lookupRunnerUser = func(string) (*user.User, error) {
		return nil, errors.New("runner user missing")
	}
	t.Cleanup(func() { lookupRunnerUser = user.Lookup })

	// When
	_, err := newSandbox()

	// Then
	if err == nil {
		t.Fatal("new sandbox succeeded without a runner identity")
	}
}

func TestSandbox_FailsClosed_WhenCapabilityDropFails(t *testing.T) {
	// Given
	lookupSetpriv = func(string) (string, error) {
		return "", errors.New("setpriv missing")
	}
	t.Cleanup(func() { lookupSetpriv = exec.LookPath })
	sandbox := &Sandbox{enabled: true}
	cmd := exec.Command("bash", "/script.sh")

	// When
	err := sandbox.clearCapabilities(cmd, false)

	// Then
	if err == nil {
		t.Fatal("clear capabilities succeeded without setpriv")
	}
}

func TestSandbox_ConfiguresCgroupFD_BeforeFork(t *testing.T) {
	// Given
	dir, err := os.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open cgroup fixture: %v", err)
	}
	t.Cleanup(func() { _ = dir.Close() })
	cmd := exec.Command("bash", "/script.sh")
	sandbox := &Sandbox{enabled: true}

	// When
	err = sandbox.configureCgroup(cmd, &cgroup{path: dir.Name(), dir: dir})

	// Then
	if err != nil {
		t.Fatalf("configure cgroup: %v", err)
	}
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.UseCgroupFD {
		t.Fatal("UseCgroupFD = false, want atomic cgroup placement")
	}
	if cmd.SysProcAttr.CgroupFD != int(dir.Fd()) {
		t.Fatalf("CgroupFD = %d, want %d", cmd.SysProcAttr.CgroupFD, dir.Fd())
	}
}

func TestClearCapabilitiesWrapsStepWithSetpriv(t *testing.T) {
	cmd := exec.Command("bash", "/script.sh")
	sandbox := &Sandbox{enabled: true}
	if err := sandbox.clearCapabilities(cmd, false); err != nil {
		t.Fatal(err)
	}

	want := []string{
		"setpriv", "--bounding-set=-all", "--inh-caps=-all",
		"--ambient-caps=-all", "--no-new-privs", "--",
		"bash", "/script.sh",
	}
	if !slices.Equal(cmd.Args, want) {
		t.Fatalf("args = %q, want %q", cmd.Args, want)
	}
}

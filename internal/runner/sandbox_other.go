//go:build !linux

package runner

import (
	"fmt"
	"os/exec"
)

// runnerUsername mirrors sandbox_linux.go's constant so runner.go can
// reference it regardless of platform (used for the USER/LOGNAME env vars).
const runnerUsername = "durpdeploy-runner"

// Sandbox is a no-op on non-Linux platforms (cgroups/chroot/credential
// dropping via SysProcAttr are Linux-specific). Steps run as the server's
// own user, same as before P1-4. See sandbox_linux.go for the real
// implementation.
type Sandbox struct{}

type cgroup struct{}

func newSandbox() (*Sandbox, error) {
	return nil, fmt.Errorf("runner sandbox requires Linux")
}

func (s *Sandbox) applyCredential(cmd *exec.Cmd) {}

func (s *Sandbox) clearCapabilities(cmd *exec.Cmd, chrooted bool) error {
	return nil
}

func (s *Sandbox) createCgroup(deploymentID int64) (*cgroup, error) {
	return nil, fmt.Errorf("runner sandbox requires Linux")
}

func (s *Sandbox) configureCgroup(cmd *exec.Cmd, group *cgroup) error {
	return fmt.Errorf("runner sandbox requires Linux")
}

func (s *Sandbox) removeCgroup(group *cgroup) error { return nil }

func (s *Sandbox) setupChroot(scratchRoot string) (bool, error) {
	return false, fmt.Errorf("runner sandbox requires Linux")
}

func (s *Sandbox) applyChroot(cmd *exec.Cmd, scratchRoot string) {}

func (s *Sandbox) teardownChroot(scratchRoot string) {}

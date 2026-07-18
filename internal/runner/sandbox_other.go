//go:build !linux

package runner

import "os/exec"

// runnerUsername mirrors sandbox_linux.go's constant so runner.go can
// reference it regardless of platform (used for the USER/LOGNAME env vars).
const runnerUsername = "durpdeploy-runner"

// Sandbox is a no-op on non-Linux platforms (cgroups/chroot/credential
// dropping via SysProcAttr are Linux-specific). Steps run as the server's
// own user, same as before P1-4. See sandbox_linux.go for the real
// implementation.
type Sandbox struct{}

func newSandbox() *Sandbox { return &Sandbox{} }

func (s *Sandbox) applyCredential(cmd *exec.Cmd) {}

func (s *Sandbox) createCgroup(deploymentID int64) string { return "" }

func (s *Sandbox) addProcess(cgroup string, pid int) {}

func (s *Sandbox) removeCgroup(cgroup string) {}

func (s *Sandbox) setupChroot(scratchRoot string) bool { return false }

func (s *Sandbox) applyChroot(cmd *exec.Cmd, scratchRoot string) {}

func (s *Sandbox) teardownChroot(scratchRoot string) {}

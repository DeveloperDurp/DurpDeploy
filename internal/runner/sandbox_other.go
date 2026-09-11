//go:build !linux

package runner

import "os/exec"

// runnerUsername mirrors sandbox_linux.go's constant so runner.go can
// reference it regardless of platform (used for the USER/LOGNAME env vars).
const runnerUsername = "durpdeploy-runner"

// Sandbox supports development-only execution on non-Linux platforms.
type Sandbox struct{}

func newSandbox() (*Sandbox, error) {
	return &Sandbox{}, nil
}

func (s *Sandbox) applyCredential(cmd *exec.Cmd) {}

func (s *Sandbox) clearCapabilities(cmd *exec.Cmd) error {
	return nil
}

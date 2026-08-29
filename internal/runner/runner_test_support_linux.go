//go:build linux

package runner

import (
	"os"
	"os/exec"

	"durpdeploy/internal/repository"
)

// NewForTests returns a runner whose executor avoids OS sandbox setup so
// handler integration tests can exercise deployment behavior without root.
func NewForTests(
	repo *repository.Repository,
	broker *LogBroker,
) *DeploymentRunner {
	runner := New(repo, broker)
	runner.executor = &Executor{sandbox: &Sandbox{
		uid:                 uint32(os.Getuid()),
		gid:                 uint32(os.Getgid()),
		enabled:             true,
		applyCredentialFn:   func(*exec.Cmd) {},
		clearCapabilitiesFn: func(*exec.Cmd, bool) error { return nil },
		createCgroupFn:      func(int64) (*cgroup, error) { return &cgroup{}, nil },
		configureCgroupFn:   func(*exec.Cmd, *cgroup) error { return nil },
		removeCgroupFn:      func(*cgroup) error { return nil },
		setupChrootFn:       func(string) (bool, error) { return false, nil },
	}}
	return runner
}

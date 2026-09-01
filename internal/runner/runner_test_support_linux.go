//go:build linux

package runner

import (
	"durpdeploy/internal/repository"

	agentexecutor "github.com/DeveloperDurp/durpdeploy-agent/executor"
)

// NewForTests returns a runner whose executor avoids OS sandbox setup so
// handler integration tests can exercise deployment behavior without root.
func NewForTests(
	repo *repository.Repository,
	broker *LogBroker,
) *DeploymentRunner {
	runner := New(repo, broker)
	runner.executor = agentexecutor.NewExecutorForTest()
	return runner
}

package runner

import (
	"context"
	"fmt"

	"durpdeploy/internal/events"
)

// SetEventBus wires the runner to publish deployment lifecycle events. Kept
// as a setter (instead of a New parameter) so every existing call site does
// not need to change; only cmd/server/main.go's real server startup calls it.
func (r *DeploymentRunner) SetEventBus(bus *events.Bus) {
	r.bus = bus
}

// KillAll SIGKILLs the process group of every step currently running,
// reaping their bash children so a server shutdown/restart never leaves
// orphaned deploy processes behind (P1-3). Safe to call with no deployments
// running.
func (r *DeploymentRunner) KillAll() {
	r.mu.Lock()
	pgids := make([]int, 0, len(r.pgids))
	for _, pgid := range r.pgids {
		pgids = append(pgids, pgid)
	}
	r.mu.Unlock()

	for _, pgid := range pgids {
		killProcessGroup(pgid)
	}
}

func (r *DeploymentRunner) Broker() *LogBroker {
	return r.broker
}

func (r *DeploymentRunner) trackProcessGroup(deploymentID int64, pgid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pgids[deploymentID] = pgid
}

func (r *DeploymentRunner) untrackProcessGroup(deploymentID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.pgids, deploymentID)
}

func (r *DeploymentRunner) RegisterCancel(
	deploymentID int64,
	cancel context.CancelFunc,
) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cancels[deploymentID] = cancel
}

func (r *DeploymentRunner) UnregisterCancel(deploymentID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.cancels, deploymentID)
}

func (r *DeploymentRunner) Cancel(deploymentID int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	cancel, ok := r.cancels[deploymentID]
	if !ok {
		return fmt.Errorf("deployment %d is not running", deploymentID)
	}

	cancel()
	return nil
}

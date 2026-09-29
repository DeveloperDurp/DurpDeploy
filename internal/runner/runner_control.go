package runner

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"durpdeploy/internal/events"
)

// SetEventBus wires the runner to publish deployment lifecycle events. Kept
// as a setter (instead of a New parameter) so every existing call site does
// not need to change; only cmd/server/main.go's real server startup calls it.
func (r *DeploymentRunner) SetEventBus(bus *events.Bus) {
	r.bus = bus
}

// KillAll cancels local attempts and forcibly removes their remote containers.
func (r *DeploymentRunner) KillAll() {
	r.mu.Lock()
	names := make([]string, 0, len(r.attempts))
	for id, name := range r.attempts {
		names = append(names, name)
		if cancel := r.cancels[id]; cancel != nil {
			cancel()
		}
	}
	r.mu.Unlock()
	for _, name := range names {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		output, err := r.engine.command(ctx, r.engine.removeArgs(name)...).
			CombinedOutput()
		cancel()
		if err != nil {
			slog.Error(
				"remove container on shutdown failed",
				"name",
				name,
				"err",
				err,
				"output",
				strings.TrimSpace(string(output)),
			)
		}
	}
}

func (r *DeploymentRunner) Broker() *LogBroker {
	return r.broker
}

func (r *DeploymentRunner) trackAttempt(deploymentID int64, name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.attempts[deploymentID] = name
}

func (r *DeploymentRunner) untrackAttempt(deploymentID int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.attempts, deploymentID)
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

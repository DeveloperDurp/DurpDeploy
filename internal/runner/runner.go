package runner

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
)

type DeploymentRunner struct {
	repo     *repository.Repository
	broker   *LogBroker
	executor *Executor
	mu       sync.Mutex
	cancels  map[int64]context.CancelFunc
	// ponytail: deployments execute steps sequentially, so one pgid per
	// deployment is sufficient; use per-step entries if parallel execution lands.
	pgids map[int64]int
	bus   *events.Bus
}

func New(repo *repository.Repository, broker *LogBroker) *DeploymentRunner {
	return &DeploymentRunner{
		repo:     repo,
		broker:   broker,
		executor: NewExecutor(),
		cancels:  make(map[int64]context.CancelFunc),
		pgids:    make(map[int64]int),
	}
}

func (r *DeploymentRunner) SetEventBus(bus *events.Bus) {
	r.bus = bus
}

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
	cancel, ok := r.cancels[deploymentID]
	r.mu.Unlock()
	if !ok {
		return fmt.Errorf("deployment %d is not running", deploymentID)
	}
	cancel()
	return r.repo.Queries.UpdateDeploymentStatus(
		context.Background(),
		db.UpdateDeploymentStatusParams{
			ID:         deploymentID,
			Status:     "cancelled",
			StartedAt:  sql.NullInt64{},
			FinishedAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		},
	)
}

func (r *DeploymentRunner) publish(
	ctx context.Context,
	typ events.Type,
	deploymentID, projectID, environmentID int64,
	message string,
) {
	if r.bus == nil {
		return
	}
	r.bus.Publish(ctx, events.Event{
		Type:          typ,
		DeploymentID:  deploymentID,
		ProjectID:     projectID,
		EnvironmentID: environmentID,
		Message:       message,
	})
}

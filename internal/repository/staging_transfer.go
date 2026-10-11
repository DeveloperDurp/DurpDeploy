package repository

import (
	"sync"
	"time"

	"durpdeploy/internal/db"
)

type stagingTransferState struct {
	mu   sync.Mutex
	runs map[db.GetStagingSnapshotParams]*stagingTransfer
}

type stagingTransfer struct {
	active  int
	started time.Time
	elapsed time.Duration
}

// BeginStagingTransfer counts the union of concurrent requests for one run.
// The caller must defer the returned completion function.
func (r *Repository) BeginStagingTransfer(
	key db.GetStagingSnapshotParams,
) func() {
	transfers := r.stagingTransfers
	transfers.mu.Lock()
	if transfers.runs == nil {
		transfers.runs = make(
			map[db.GetStagingSnapshotParams]*stagingTransfer,
		)
	}
	transfer := transfers.runs[key]
	if transfer == nil {
		transfer = &stagingTransfer{}
		transfers.runs[key] = transfer
	}
	if transfer.active == 0 {
		transfer.started = time.Now()
	}
	transfer.active++
	transfers.mu.Unlock()
	return func() {
		transfers.mu.Lock()
		defer transfers.mu.Unlock()
		transfer.active--
		if transfer.active == 0 {
			transfer.elapsed += time.Since(transfer.started)
		}
	}
}

func (r *Repository) StagingTransferDuration(
	key db.GetStagingSnapshotParams, now time.Time,
) time.Duration {
	transfers := r.stagingTransfers
	transfers.mu.Lock()
	defer transfers.mu.Unlock()
	transfer := transfers.runs[key]
	if transfer == nil {
		return 0
	}
	elapsed := transfer.elapsed
	if transfer.active > 0 {
		elapsed += max(0, now.Sub(transfer.started))
	}
	return elapsed
}

func (r *Repository) ClearStagingTransfers(
	key db.ListRemoteStepRunsForRunnerParams,
) {
	transfers := r.stagingTransfers
	transfers.mu.Lock()
	defer transfers.mu.Unlock()
	for run := range transfers.runs {
		if run.DeploymentID == key.DeploymentID &&
			run.StepIndex == key.StepIndex {
			delete(transfers.runs, run)
		}
	}
}

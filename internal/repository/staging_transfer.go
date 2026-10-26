package repository

import (
	"sync"
	"time"

	"durpdeploy/internal/db"
)

type stagingTransferState struct {
	mu       sync.Mutex
	runs     map[db.GetStagingSnapshotParams]*stagingTransfer
	admitted map[string]int64
	active   int64
}

type stagingTransfer struct {
	active  int
	started time.Time
	elapsed time.Duration
}

// TryBeginStagingTransfer applies the captured global and per-agent budgets.
// Rejected work does not wait or pause the run's timeout.
// The caller must defer the returned completion function after admission.
func (r *Repository) TryBeginStagingTransfer(
	key db.GetStagingSnapshotParams,
) (func(), bool) {
	transfers := r.stagingTransfers
	limits := r.ResourceLimits().Transfer
	transfers.mu.Lock()
	if transfers.admitted[key.Producer] >= limits.PerAgent ||
		transfers.active >= limits.Global {
		transfers.mu.Unlock()
		return nil, false
	}
	if transfers.admitted == nil {
		transfers.admitted = make(map[string]int64)
	}
	transfers.admitted[key.Producer]++
	transfers.active++
	transfers.mu.Unlock()
	finish := r.BeginStagingTransfer(key)
	var once sync.Once
	return func() {
		once.Do(func() {
			finish()
			transfers.mu.Lock()
			transfers.active--
			transfers.admitted[key.Producer]--
			if transfers.admitted[key.Producer] == 0 {
				delete(transfers.admitted, key.Producer)
			}
			transfers.mu.Unlock()
		})
	}, true
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

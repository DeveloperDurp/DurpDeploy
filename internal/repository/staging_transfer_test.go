package repository

import (
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestStagingTransferCountsOverlappingRequestsOnce(t *testing.T) {
	repo := New(nil)
	key := db.GetStagingSnapshotParams{DeploymentID: 1, Producer: "agent"}
	started := time.Now()
	first := repo.BeginStagingTransfer(key)
	second := repo.BeginStagingTransfer(key)
	view := *repo
	now := time.Now()
	if view.StagingTransferDuration(
		key,
		now,
	) != repo.StagingTransferDuration(
		key,
		now,
	) {
		t.Fatal("repository transaction view lost shared transfer timing")
	}
	duration := repo.StagingTransferDuration(key, time.Now())
	if duration < 0 || duration > time.Since(started) {
		t.Fatalf("overlapping requests double-counted time: %s", duration)
	}
	first()
	during := repo.StagingTransferDuration(key, time.Now())
	if during < duration {
		t.Fatalf(
			"first completion lost the second request: %s < %s",
			during,
			duration,
		)
	}
	second()
	completed := repo.StagingTransferDuration(key, time.Now())
	if completed < during ||
		repo.StagingTransferDuration(
			key,
			time.Now().Add(time.Hour),
		) != completed {
		t.Fatal("completed transfer kept consuming execution time")
	}
	key.Producer = "other-agent"
	if duration := repo.StagingTransferDuration(
		key,
		time.Now(),
	); duration != 0 {
		t.Fatalf("one agent paused another agent's timeout: %s", duration)
	}
}

func TestStagingTransferCleanupDoesNotRestoreLateRequests(t *testing.T) {
	repo := New(nil)
	key := db.GetStagingSnapshotParams{DeploymentID: 1, Producer: "agent"}
	finish := repo.BeginStagingTransfer(key)
	repo.ClearStagingTransfers(
		db.ListRemoteStepRunsForRunnerParams{DeploymentID: 1},
	)
	finish()
	if duration := repo.StagingTransferDuration(
		key,
		time.Now(),
	); duration != 0 {
		t.Fatalf(
			"late completion restored terminal transfer state: %s",
			duration,
		)
	}
}

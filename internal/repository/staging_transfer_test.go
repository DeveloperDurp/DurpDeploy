package repository

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/db"
)

func TestStagingTransferAdmissionBoundsAndCleanup(t *testing.T) {
	repo := New(nil)
	view := *repo
	key := db.GetStagingSnapshotParams{DeploymentID: 1, Producer: "agent"}
	finish, ok := repo.TryBeginStagingTransfer(key)
	if !ok {
		t.Fatal("initial transfer rejected")
	}
	// Another claim for this agent still shares its one permit.
	other := key
	other.DeploymentID++
	if done, ok := view.TryBeginStagingTransfer(other); ok || done != nil {
		t.Fatal("second claim bypassed the per-agent bound")
	}
	if repo.StagingTransferDuration(other, time.Now()) != 0 {
		t.Fatal("rejected transfer paused an unrelated run")
	}
	var admitted []func()
	for i := range 3 {
		other.Producer = fmt.Sprintf("other-%d", i)
		done, ok := view.TryBeginStagingTransfer(other)
		if !ok {
			t.Fatal("independent agent rejected before global capacity")
		}
		admitted = append(admitted, done)
	}
	other.Producer = "overflow"
	if done, ok := repo.TryBeginStagingTransfer(other); ok || done != nil {
		t.Fatal("fifth transfer bypassed the global bound")
	}
	// Terminal timing cleanup must not release active I/O admission.
	repo.ClearStagingTransfers(db.ListRemoteStepRunsForRunnerParams{
		DeploymentID: key.DeploymentID,
	})
	if done, ok := view.TryBeginStagingTransfer(key); ok || done != nil {
		t.Fatal("timing cleanup released an active permit")
	}
	finish()
	finish() // Repeated completion cannot release another agent's permit.
	if repo.StagingTransferDuration(key, time.Now()) != 0 {
		t.Fatal("late completion restored terminal timing state")
	}
	done, ok := view.TryBeginStagingTransfer(other)
	if !ok {
		t.Fatal("completion did not release the global permit")
	}
	done()
	for _, done := range admitted {
		done()
	}
	if len(repo.stagingTransfers.admitted) != 0 {
		t.Fatal("completed agent IDs retained in admission map")
	}
}

func TestStagingTransferAdmissionConcurrentRequests(t *testing.T) {
	for _, limits := range []config.TransferLimits{
		config.Defaults().Limits.Transfer,
		{Global: 2, PerAgent: 1, IdleTimeoutSeconds: 1},
		{Global: 6, PerAgent: 3, IdleTimeoutSeconds: 300},
	} {
		for _, sharedAgent := range []bool{true, false} {
			t.Run(
				fmt.Sprintf(
					"%d/%d/same-agent=%v",
					limits.Global,
					limits.PerAgent,
					sharedAgent,
				),
				func(t *testing.T) {
					testStagingTransferAdmissionConcurrentRequests(
						t,
						sharedAgent,
						limits,
					)
				},
			)
		}
	}
}

func testStagingTransferAdmissionConcurrentRequests(
	t *testing.T,
	sharedAgent bool,
	limits config.TransferLimits,
) {
	t.Helper()
	settings := config.Defaults()
	settings.Limits.Transfer = limits
	repo := NewConfigured(nil, settings)
	start := make(chan struct{})
	results := make(chan func(), 64)
	var workers sync.WaitGroup
	for i := range 64 {
		workers.Go(func() {
			<-start
			agent := "agent"
			if !sharedAgent {
				agent = fmt.Sprintf("agent-%d", i)
			}
			done, ok := repo.TryBeginStagingTransfer(
				db.GetStagingSnapshotParams{
					DeploymentID: int64(i), Producer: agent,
				},
			)
			if ok {
				results <- done
			}
		})
	}
	close(start)
	workers.Wait()
	close(results)
	want := limits.Global
	if sharedAgent {
		want = limits.PerAgent
	}
	if int64(len(results)) != want {
		t.Fatalf("admitted=%d want=%d", len(results), want)
	}
	for done := range results {
		done()
	}
	if len(repo.stagingTransfers.admitted) != 0 ||
		repo.stagingTransfers.active != 0 {
		t.Fatal("admission state retained after concurrent completion")
	}
}

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

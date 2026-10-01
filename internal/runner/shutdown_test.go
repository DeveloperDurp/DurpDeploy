package runner

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRunAfterShutdownDoesNotStart(t *testing.T) {
	// Given: a stopped runner with no repository or execution resources.
	r := &DeploymentRunner{
		cancels:  make(map[int64]context.CancelFunc),
		attempts: make(map[int64]string),
	}
	r.KillAll()

	// When: an already-launched goroutine reaches Run after shutdown.
	defer func() {
		// Then: it must return before touching the repository or runtime.
		if failure := recover(); failure != nil {
			t.Errorf("late runner started after shutdown: %v", failure)
		}
	}()
	r.Run(t.Context(), 1, 1, 1)
}

func TestLocalAttemptAfterShutdownDoesNotStart(t *testing.T) {
	// Given: a stopped runner with no execution resources.
	r := &DeploymentRunner{
		cancels:  make(map[int64]context.CancelFunc),
		attempts: make(map[int64]string),
	}
	r.KillAll()

	// When: a previously admitted Run reaches local execution after shutdown.
	err := r.runStepAttempt(t.Context(), localStepAttempt{})

	// Then: local admission fails before contacting the runtime.
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("late local attempt was not cancelled: %v", err)
	}
}

func TestShutdownWaitsForRunnerCleanup(t *testing.T) {
	// Given: an admitted runner whose cleanup is not finished yet.
	r := &DeploymentRunner{
		cancels:  make(map[int64]context.CancelFunc),
		attempts: make(map[int64]string),
	}
	if !r.beginLocalWork() {
		t.Fatal("local work was not admitted")
	}
	cancelled := make(chan struct{})
	r.RegisterCancel(1, func() { close(cancelled) })
	done := make(chan struct{})

	// When: shutdown cancels the runner.
	go func() {
		r.KillAll()
		close(done)
	}()
	<-cancelled
	select {
	case <-done:
		t.Error("shutdown returned before runner cleanup")
	case <-time.After(50 * time.Millisecond):
	}
	r.localWork.Done()

	// Then: shutdown returns only once cleanup has finished.
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown did not finish after cleanup")
	}
}

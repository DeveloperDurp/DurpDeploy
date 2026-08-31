package runner

import (
	"context"
	"testing"
)

func TestExecutor_SkipsDeadlineCleanup_when_attempt_succeeds(t *testing.T) {
	// Given
	executor := newExecutorForTest(t)
	graceCalled := make(chan struct{}, 1)
	killCalled := make(chan int, 1)
	executor.deadlineGrace = func() { graceCalled <- struct{}{} }
	executor.killGroup = func(pgid int) { killCalled <- pgid }
	job := NewJob(JobConfig{Name: "success", ScriptBody: "exit 0"})

	// When
	err := executor.Execute(context.Background(), job, Callbacks{})

	// Then
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	select {
	case <-graceCalled:
		t.Fatal("deadline cleanup started after successful attempt")
	default:
	}
	select {
	case pgid := <-killCalled:
		t.Fatalf("killed process group %d after successful attempt", pgid)
	default:
	}
}

func TestExecutor_KillsCapturedProcessGroup_when_attempt_cancelled(t *testing.T) {
	// Given
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	executor := newExecutorForTest(t)
	graceStarted := make(chan struct{})
	releaseGrace := make(chan struct{})
	killCalled := make(chan int, 1)
	processStarted := make(chan struct{})
	executor.deadlineGrace = func() {
		close(graceStarted)
		<-releaseGrace
	}
	executor.killGroup = func(pgid int) { killCalled <- pgid }
	job := NewJob(JobConfig{Name: "cancel", ScriptBody: "sleep 30"})
	executeDone := make(chan error, 1)
	go func() {
		executeDone <- executor.Execute(ctx, job, NewCallbacks(CallbacksConfig{
			TrackProcessGroup: func(int) { close(processStarted) },
		}))
	}()

	// When
	<-processStarted
	cancel()
	<-graceStarted
	close(releaseGrace)
	err := <-executeDone

	// Then
	if err == nil {
		t.Fatal("execute succeeded after cancellation")
	}
	select {
	case pgid := <-killCalled:
		if pgid <= 0 {
			t.Fatalf("killed process group = %d, want captured positive ID", pgid)
		}
	default:
		t.Fatal("deadline cleanup did not kill the captured process group")
	}
}

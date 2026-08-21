package agentproto

import (
	"errors"
	"testing"
)

func TestDispatchState_Complete_rejects_duplicate_result(t *testing.T) {
	// Given
	state := DispatchSucceeded

	// When
	_, err := Complete(state, ResultSucceeded)

	// Then
	if !errors.Is(err, ErrDuplicateResult) {
		t.Fatalf("Complete() error = %v, want %v", err, ErrDuplicateResult)
	}
}

func TestDispatchState_Reclaim_rejects_started_work_replay(t *testing.T) {
	// Given
	state := DispatchStarted

	// When
	_, err := Reclaim(state)

	// Then
	if !errors.Is(err, ErrStartedWorkReplay) {
		t.Fatalf("Reclaim() error = %v, want %v", err, ErrStartedWorkReplay)
	}
}

package agentproto

import (
	"errors"
	"testing"
)

func TestDispatchState_Transition_allows_contract_edges(t *testing.T) {
	tests := []struct {
		name string
		from DispatchState
		to   DispatchState
	}{
		{"waiting to claimed", DispatchWaiting, DispatchClaimed},
		{"claimed reclaim to waiting", DispatchClaimed, DispatchWaiting},
		{"claimed to started", DispatchClaimed, DispatchStarted},
		{
			"claimed cancellation requested",
			DispatchClaimed,
			DispatchCancelRequested,
		},
		{"started to succeeded", DispatchStarted, DispatchSucceeded},
		{"started to failed", DispatchStarted, DispatchFailed},
		{"started to cancelled", DispatchStarted, DispatchCancelled},
		{"started to lost", DispatchStarted, DispatchLost},
		{
			"started cancellation requested",
			DispatchStarted,
			DispatchCancelRequested,
		},
		{
			"cancel requested to cancelled",
			DispatchCancelRequested,
			DispatchCancelled,
		},
		{
			"cancel requested to unconfirmed",
			DispatchCancelRequested,
			DispatchCancelUnconfirmed,
		},
		{"cancel requested to lost", DispatchCancelRequested, DispatchLost},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			from, to := test.from, test.to

			// When
			got, err := Transition(from, to)

			// Then
			if err != nil {
				t.Fatalf("Transition(%q, %q): %v", from, to, err)
			}
			if got != to {
				t.Fatalf("Transition(%q, %q) = %q, want %q", from, to, got, to)
			}
		})
	}
}

func TestDispatchState_Transition_rejects_invalid_edges(t *testing.T) {
	tests := []struct {
		name string
		from DispatchState
		to   DispatchState
	}{
		{"waiting skips claim", DispatchWaiting, DispatchStarted},
		{"started to waiting", DispatchStarted, DispatchWaiting},
		{
			"terminal state does not transition",
			DispatchSucceeded,
			DispatchWaiting,
		},
		{"claimed does not become lost", DispatchClaimed, DispatchLost},
		{"unknown source", DispatchState("unknown"), DispatchClaimed},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			from, to := test.from, test.to

			// When
			_, err := Transition(from, to)

			// Then
			if !errors.Is(err, ErrInvalidTransition) {
				t.Fatalf(
					"Transition(%q, %q) error = %v, want %v",
					from,
					to,
					err,
					ErrInvalidTransition,
				)
			}
		})
	}
}

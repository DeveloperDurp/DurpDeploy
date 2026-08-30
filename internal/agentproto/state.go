package agentproto

type DispatchState string

const (
	DispatchWaiting           DispatchState = "waiting"
	DispatchClaimed           DispatchState = "claimed"
	DispatchStarted           DispatchState = "started"
	DispatchSucceeded         DispatchState = "succeeded"
	DispatchFailed            DispatchState = "failed"
	DispatchCancelled         DispatchState = "cancelled"
	DispatchLost              DispatchState = "lost"
	DispatchCancelRequested   DispatchState = "cancel_requested"
	DispatchCancelUnconfirmed DispatchState = "cancel_unconfirmed"
)

func Transition(from, to DispatchState) (DispatchState, error) {
	if !CanTransition(from, to) {
		return "", protocolError(
			"state",
			ReasonInvalid,
			ErrInvalidTransition,
		)
	}
	return to, nil
}

func CanTransition(from, to DispatchState) bool {
	switch from {
	case DispatchWaiting:
		return to == DispatchClaimed || to == DispatchCancelled
	case DispatchClaimed:
		return to == DispatchWaiting || to == DispatchStarted ||
			to == DispatchCancelRequested || to == DispatchCancelled
	case DispatchStarted:
		return to == DispatchSucceeded || to == DispatchFailed ||
			to == DispatchCancelled || to == DispatchLost ||
			to == DispatchCancelRequested
	case DispatchCancelRequested:
		return to == DispatchCancelled || to == DispatchCancelUnconfirmed ||
			to == DispatchLost
	default:
		return false
	}
}

// Complete applies a terminal result exactly once after work has started.
func Complete(
	state DispatchState,
	result ResultState,
) (DispatchState, error) {
	switch state {
	case DispatchSucceeded, DispatchFailed:
		return "", protocolError("result", ReasonDuplicate, ErrDuplicateResult)
	case DispatchStarted:
		switch result {
		case ResultSucceeded:
			return Transition(state, DispatchSucceeded)
		case ResultFailed:
			return Transition(state, DispatchFailed)
		default:
			return "", protocolError(
				"result.state",
				ReasonInvalid,
				ErrInvalidResultState,
			)
		}
	default:
		return "", protocolError("result", ReasonInvalid, ErrInvalidTransition)
	}
}

// Reclaim returns a pre-start claim to the waiting queue. Started work is
// intentionally never replayed because deployment scripts need not be idempotent.
func Reclaim(state DispatchState) (DispatchState, error) {
	if state == DispatchStarted {
		return "", protocolError("state", ReasonInvalid, ErrStartedWorkReplay)
	}
	return Transition(state, DispatchWaiting)
}

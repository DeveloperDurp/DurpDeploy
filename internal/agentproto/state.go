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
		return to == DispatchClaimed
	case DispatchClaimed:
		return to == DispatchWaiting || to == DispatchStarted ||
			to == DispatchCancelRequested
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

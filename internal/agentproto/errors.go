package agentproto

import "errors"

var (
	ErrUnsupportedProtocol  = errors.New("agent protocol: unsupported version")
	ErrInvalidSelector      = errors.New("agent protocol: invalid selector")
	ErrDuplicateSelectorKey = errors.New(
		"agent protocol: duplicate selector key",
	)
	ErrSelectorLimit = errors.New(
		"agent protocol: selector key limit exceeded",
	)
	ErrUnknownField     = errors.New("agent protocol: unknown JSON field")
	ErrInvalidJSON      = errors.New("agent protocol: invalid JSON")
	ErrRequestTooLarge  = errors.New("agent protocol: request too large")
	ErrLogBatchTooLarge = errors.New("agent protocol: log batch too large")
	ErrLogEventLimit    = errors.New(
		"agent protocol: log event limit exceeded",
	)
	ErrLogLineTooLarge   = errors.New("agent protocol: log line too large")
	ErrInvalidTransition = errors.New(
		"agent protocol: invalid state transition",
	)
	ErrInvalidResultState = errors.New("agent protocol: invalid result state")
)

type ErrorReason string

const (
	ReasonInvalid   ErrorReason = "invalid"
	ReasonDuplicate ErrorReason = "duplicate"
	ReasonLimit     ErrorReason = "limit"
	ReasonUnknown   ErrorReason = "unknown"
)

// ProtocolError identifies a rejected untrusted protocol field without
// retaining its value.
type ProtocolError struct {
	Field  string
	Reason ErrorReason
	cause  error
}

func (e *ProtocolError) Error() string {
	return "agent protocol: " + e.Field + " (" + string(e.Reason) + ")"
}

func (e *ProtocolError) Is(target error) bool {
	return target == e.cause
}

func protocolError(field string, reason ErrorReason, cause error) error {
	return &ProtocolError{Field: field, Reason: reason, cause: cause}
}

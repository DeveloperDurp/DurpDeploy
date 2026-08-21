package agentproto

import "errors"

var (
	ErrUnsupportedProtocol = errors.New("agent protocol: unsupported version")
	ErrUnknownField        = errors.New("agent protocol: unknown JSON field")
	ErrInvalidJSON         = errors.New("agent protocol: invalid JSON")
	ErrRequestTooLarge     = errors.New("agent protocol: request too large")
	ErrLogBatchTooLarge    = errors.New("agent protocol: log batch too large")
	ErrLogEventLimit       = errors.New(
		"agent protocol: log event limit exceeded",
	)
	ErrLogLineTooLarge   = errors.New("agent protocol: log line too large")
	ErrInvalidTransition = errors.New(
		"agent protocol: invalid state transition",
	)
	ErrInvalidResultState = errors.New("agent protocol: invalid result state")
	ErrInvalidPairingCode = errors.New("agent protocol: invalid pairing code")
	ErrPairingCodeExpired = errors.New("agent protocol: pairing code expired")
	ErrPairingCodeUsed    = errors.New(
		"agent protocol: pairing code already used",
	)
	ErrPairingCodeMismatch = errors.New(
		"agent protocol: pairing code does not match",
	)
	ErrInvalidSHA256Pin = errors.New(
		"agent protocol: invalid SHA-256 pin",
	)
	ErrAgentFingerprintMismatch = errors.New(
		"agent protocol: agent fingerprint does not match",
	)
	ErrInvalidPullEndpoint = errors.New(
		"agent protocol: invalid pull endpoint",
	)
	ErrInvalidPairingOffer = errors.New(
		"agent protocol: invalid pairing offer",
	)
	ErrInvalidPairingConfirmation = errors.New(
		"agent protocol: invalid pairing confirmation",
	)
	ErrDuplicateResult = errors.New(
		"agent protocol: duplicate result",
	)
	ErrStartedWorkReplay = errors.New(
		"agent protocol: started work cannot be replayed",
	)
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

package agentproto

import (
	"sync"
	"time"
)

type PairingState string

const (
	PairingPending PairingState = "pending"
	PairingPaired  PairingState = "paired"
	PairingExpired PairingState = "expired"
)

// PairingOffer contains the temporary agent identity and code observed at
// bootstrap. The session retains only the code hash.
type PairingOffer struct {
	Code      PairingCode
	AgentPin  SHA256Pin
	ExpiresAt time.Time
}

// PairingConfirmation carries the values required to atomically establish
// mutual pins and the outbound-only pull endpoint.
type PairingConfirmation struct {
	Code          PairingCode
	ObservedAgent SHA256Pin
	ServerPin     SHA256Pin
	PullEndpoint  PullEndpoint
}

type MutualTrust struct {
	Agent  SHA256Pin
	Server SHA256Pin
}

// PairingSession is a durable-state contract. It never retains the plaintext
// pairing code, only its SHA-256 hash.
type PairingSession struct {
	current *pairingSessionState
}

type pairingSessionState struct {
	mu            sync.Mutex
	codeHash      PairingCodeHash
	expiresAt     time.Time
	expectedAgent SHA256Pin
	status        PairingState
	trust         MutualTrust
	pullEndpoint  PullEndpoint
}

func NewPairingSession(offer PairingOffer) (PairingSession, error) {
	if !offer.Code.valid || !offer.AgentPin.valid || offer.ExpiresAt.IsZero() {
		return PairingSession{}, protocolError(
			"pairing_offer",
			ReasonInvalid,
			ErrInvalidPairingOffer,
		)
	}
	return PairingSession{
		current: &pairingSessionState{
			codeHash:      offer.Code.Hash(),
			expiresAt:     offer.ExpiresAt,
			expectedAgent: offer.AgentPin,
			status:        PairingPending,
		},
	}, nil
}

func (s PairingSession) State() PairingState {
	if s.current == nil {
		return ""
	}
	s.current.mu.Lock()
	defer s.current.mu.Unlock()
	return s.current.status
}

// Expire atomically consumes a pending offer whose expiry has passed.
func (s PairingSession) Expire(now time.Time) bool {
	if s.current == nil {
		return false
	}
	s.current.mu.Lock()
	defer s.current.mu.Unlock()
	if s.current.status != PairingPending || now.Before(s.current.expiresAt) {
		return false
	}
	s.current.status = PairingExpired
	return true
}

func (s PairingSession) CodeHash() PairingCodeHash {
	if s.current == nil {
		return PairingCodeHash{}
	}
	s.current.mu.Lock()
	defer s.current.mu.Unlock()
	return s.current.codeHash
}

func (s PairingSession) Trust() (MutualTrust, bool) {
	if s.current == nil {
		return MutualTrust{}, false
	}
	s.current.mu.Lock()
	defer s.current.mu.Unlock()
	return s.current.trust, s.current.status == PairingPaired
}

func (s PairingSession) PullEndpoint() (PullEndpoint, bool) {
	if s.current == nil {
		return PullEndpoint{}, false
	}
	s.current.mu.Lock()
	defer s.current.mu.Unlock()
	return s.current.pullEndpoint, s.current.status == PairingPaired
}

func (s PairingSession) Pair(
	now time.Time,
	confirmation PairingConfirmation,
) (PairingSession, error) {
	return s.pair(now, confirmation, nil)
}

func (s PairingSession) Prepare(
	now time.Time,
	confirmation PairingConfirmation,
) error {
	if s.current == nil {
		return protocolError("pairing_session", ReasonInvalid, ErrInvalidPairingOffer)
	}
	s.current.mu.Lock()
	defer s.current.mu.Unlock()
	if s.current.status != PairingPending {
		return protocolError("pairing_code", ReasonDuplicate, ErrPairingCodeUsed)
	}
	if !now.Before(s.current.expiresAt) {
		return protocolError("pairing_code", ReasonInvalid, ErrPairingCodeExpired)
	}
	if !confirmation.Code.valid || !s.current.codeHash.matches(confirmation.Code) {
		return protocolError("pairing_code", ReasonInvalid, ErrPairingCodeMismatch)
	}
	if !s.current.expectedAgent.matches(confirmation.ObservedAgent) {
		return protocolError("agent_pin", ReasonInvalid, ErrAgentFingerprintMismatch)
	}
	if !confirmation.ServerPin.valid || !confirmation.PullEndpoint.valid() {
		return protocolError("pairing_confirmation", ReasonInvalid, ErrInvalidPairingConfirmation)
	}
	return nil
}

// PairAndCommit consumes the pairing code only after commit durably stores the
// paired state. A failed commit leaves the session pending for a retry.
func (s PairingSession) PairAndCommit(
	now time.Time,
	confirmation PairingConfirmation,
	commit func() error,
) (PairingSession, error) {
	return s.pair(now, confirmation, commit)
}

func (s PairingSession) pair(
	now time.Time,
	confirmation PairingConfirmation,
	commit func() error,
) (PairingSession, error) {
	if s.current == nil {
		return s, protocolError(
			"pairing_session",
			ReasonInvalid,
			ErrInvalidPairingOffer,
		)
	}
	s.current.mu.Lock()
	defer s.current.mu.Unlock()

	if s.current.status == PairingPaired && commit != nil &&
		s.current.codeHash.matches(confirmation.Code) &&
		s.current.expectedAgent.matches(confirmation.ObservedAgent) &&
		s.current.trust.Server == confirmation.ServerPin &&
		s.current.pullEndpoint == confirmation.PullEndpoint {
		return s, nil
	}

	switch s.current.status {
	case PairingPaired:
		return s, protocolError(
			"pairing_code",
			ReasonDuplicate,
			ErrPairingCodeUsed,
		)
	case PairingExpired:
		return s, protocolError(
			"pairing_code",
			ReasonInvalid,
			ErrPairingCodeExpired,
		)
	case PairingPending:
	default:
		return s, protocolError(
			"pairing_session",
			ReasonInvalid,
			ErrInvalidPairingOffer,
		)
	}
	if !now.Before(s.current.expiresAt) {
		s.current.status = PairingExpired
		return s, protocolError(
			"pairing_code",
			ReasonInvalid,
			ErrPairingCodeExpired,
		)
	}
	if !confirmation.Code.valid ||
		!s.current.codeHash.matches(confirmation.Code) {
		return s, protocolError(
			"pairing_code",
			ReasonInvalid,
			ErrPairingCodeMismatch,
		)
	}
	if !s.current.expectedAgent.matches(confirmation.ObservedAgent) {
		return s, protocolError(
			"agent_pin",
			ReasonInvalid,
			ErrAgentFingerprintMismatch,
		)
	}
	if !confirmation.ServerPin.valid || !confirmation.PullEndpoint.valid() {
		return s, protocolError(
			"pairing_confirmation",
			ReasonInvalid,
			ErrInvalidPairingConfirmation,
		)
	}
	trust := MutualTrust{
		Agent:  s.current.expectedAgent,
		Server: confirmation.ServerPin,
	}
	if commit != nil {
		if err := commit(); err != nil {
			return s, err
		}
	}
	s.current.status = PairingPaired
	s.current.trust = trust
	s.current.pullEndpoint = confirmation.PullEndpoint
	return s, nil
}

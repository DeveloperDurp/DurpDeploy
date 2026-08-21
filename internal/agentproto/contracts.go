package agentproto

import (
	"encoding/json"
	"time"
)

type AgentID string
type AgentVersion string
type CertificatePEM string
type CertificateFingerprint string
type ClaimToken string
type DeploymentID int64
type LogSequence int64

type ProtocolEnvelope struct {
	Protocol ProtocolVersion `json:"protocol"`
}

func (e ProtocolEnvelope) protocolVersion() ProtocolVersion {
	return e.Protocol
}

// BootstrapResponse is the temporary listener's versioned pairing offer.
// PairingCode is intentionally only available through JSON encoding for the
// short-lived bootstrap transport.
type BootstrapResponse struct {
	ProtocolEnvelope
	PairingCode PairingCode `json:"pairing_code"`
	AgentPin    SHA256Pin   `json:"agent_pin"`
	ExpiresAt   time.Time   `json:"expires_at"`
}

// PairRequest carries the server's mutually pinned pull endpoint to the agent.
type PairRequest struct {
	ProtocolEnvelope
	PairingCode  PairingCode  `json:"pairing_code"`
	ServerPin    SHA256Pin    `json:"server_pin"`
	PullEndpoint PullEndpoint `json:"pull_endpoint"`
	AgentID      string       `json:"agent_id"`
}

func (PairRequest) agentRequest() {}

type PairCommitRequest = PairRequest

// PairResponse confirms the agent identity that the server observed during
// temporary bootstrap pairing.
type PairResponse struct {
	ProtocolEnvelope
	AgentPin SHA256Pin `json:"agent_pin"`
}

type PollRequest struct {
	ProtocolEnvelope
	AgentVersion AgentVersion `json:"agent_version"`
}

func (PollRequest) agentRequest() {}

// PollResponse carries a single encrypted deployment payload and its
// one-time claim token. The server persists only a hash of ClaimToken.
type PollResponse struct {
	DeploymentID DeploymentID `json:"deployment_id"`
	Payload      string       `json:"payload"`
	ClaimToken   ClaimToken   `json:"claim_token"`
}

type StartRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken `json:"claim_token"`
}

func (StartRequest) agentRequest() {}

type HeartbeatRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken `json:"claim_token"`
}

func (HeartbeatRequest) agentRequest() {}

type HeartbeatResponse struct {
	CancelRequested bool                     `json:"cancel_requested"`
	ServerPins      []CertificateFingerprint `json:"server_pins"`
}

type LogEvent struct {
	Sequence LogSequence `json:"sequence"`
	Line     string      `json:"line"`
}

type LogBatchRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken `json:"claim_token"`
	Events     []LogEvent `json:"events"`
}

func (LogBatchRequest) agentRequest() {}

type ResultState string

const (
	ResultSucceeded ResultState = "succeeded"
	ResultFailed    ResultState = "failed"
)

func (s *ResultState) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError("state", ReasonInvalid, ErrInvalidJSON)
	}
	if raw != string(ResultSucceeded) && raw != string(ResultFailed) {
		return protocolError("state", ReasonInvalid, ErrInvalidResultState)
	}
	*s = ResultState(raw)
	return nil
}

type ResultRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken  `json:"claim_token"`
	State      ResultState `json:"state"`
	Error      string      `json:"error"`
}

func (ResultRequest) agentRequest() {}

type CancelledRequest struct {
	ProtocolEnvelope
	ClaimToken ClaimToken `json:"claim_token"`
}

func (CancelledRequest) agentRequest() {}

type Request interface {
	agentRequest()
	protocolVersion() ProtocolVersion
}

package agentproto

import (
	"encoding/json"
	"time"
)

type ProtocolVersion string

const (
	AgentV1 ProtocolVersion = "agent/1"

	MaxRequestBytes  = 1 << 20
	MaxLogEvents     = 100
	MaxLogBatchBytes = 256 << 10
	MaxLogLineBytes  = 16 << 10

	PollInterval                 = 25 * time.Second
	HeartbeatInterval            = 10 * time.Second
	PreStartClaimTimeout         = 60 * time.Second
	LostThreshold                = 45 * time.Second
	CancelAcknowledgementTimeout = 30 * time.Second
)

const (
	EnrollmentPath = "/agent/v1/enroll"
	PollPath       = "/agent/v1/poll"
	DeploymentPath = "/agent/v1/deployments/{id}"
	StartPath      = DeploymentPath + "/start"
	HeartbeatPath  = DeploymentPath + "/heartbeat"
	LogsPath       = DeploymentPath + "/logs"
	ResultPath     = DeploymentPath + "/result"
	CancelledPath  = DeploymentPath + "/cancelled"
)

func ParseProtocolVersion(raw string) (ProtocolVersion, error) {
	if raw != string(AgentV1) {
		return "", protocolError(
			"protocol",
			ReasonInvalid,
			ErrUnsupportedProtocol,
		)
	}
	return AgentV1, nil
}

func (v *ProtocolVersion) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return protocolError("protocol", ReasonInvalid, ErrInvalidJSON)
	}

	parsed, err := ParseProtocolVersion(raw)
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

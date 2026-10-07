package dispatch

import (
	"encoding/json"
	"fmt"

	"durpdeploy/internal/runner"
	"github.com/DeveloperDurp/durpdeploy-agent/executor"
	agentpayload "github.com/DeveloperDurp/durpdeploy-agent/payload"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type Payload struct {
	DeploymentID agentproto.DeploymentID `json:"deployment_id"`
	Release      ReleaseSnapshot         `json:"release"`
	Environment  EnvironmentSnapshot     `json:"environment"`
	Variables    []VariableSnapshot      `json:"variables"`
}

type ReleaseSnapshot struct {
	ID        int64           `json:"id"`
	ProjectID int64           `json:"project_id"`
	Version   string          `json:"version"`
	Steps     []executor.Step `json:"steps"`
}

type EnvironmentSnapshot struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

type VariableSnapshot struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

func VariableSnapshots(variables []runner.ResolvedVariable) []VariableSnapshot {
	result := make([]VariableSnapshot, len(variables))
	for i, variable := range variables {
		result[i] = VariableSnapshot{
			Name:   variable.Name,
			Value:  variable.Value,
			Secret: variable.Secret,
		}
	}
	return result
}

func (snapshot Payload) Seal(certificateDER []byte) ([]byte, error) {
	plaintext, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal deployment payload: %w", err)
	}
	return agentpayload.Seal(
		certificateDER, int64(snapshot.DeploymentID), plaintext,
	)
}

func (snapshot Payload) SealForProtocol(
	certificateDER []byte,
	protocol agentproto.ProtocolVersion,
) ([]byte, error) {
	steps := make([]json.RawMessage, len(snapshot.Release.Steps))
	for index, step := range snapshot.Release.Steps {
		encoded, err := step.MarshalForProtocol(protocol)
		if err != nil {
			return nil, fmt.Errorf("encode step %q: %w", step.Name, err)
		}
		steps[index] = encoded
	}
	wire := struct {
		Payload
		Release struct {
			ReleaseSnapshot
			Steps []json.RawMessage `json:"steps"`
		} `json:"release"`
	}{Payload: snapshot}
	wire.Release.ReleaseSnapshot = snapshot.Release
	wire.Release.Steps = steps
	plaintext, err := json.Marshal(wire)
	if err != nil {
		return nil, fmt.Errorf("marshal deployment payload: %w", err)
	}
	return agentpayload.Seal(
		certificateDER,
		int64(snapshot.DeploymentID),
		plaintext,
	)
}

package dispatch

import (
	"errors"
	"slices"

	"durpdeploy/internal/repository"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

var errStaleAgentPoll = errors.New("agent poll capabilities changed")

// Compare under the claim's agent lock, before issuing a token or ciphertext.
func pollCapabilitiesMatch(
	request agentproto.PollRequest,
	snapshot repository.RemotePayloadSnapshot,
) bool {
	protocol := snapshot.Agent.AgentProtocol.String
	if protocol == "" {
		protocol = string(agentproto.AgentV1)
	}
	host, modes := request.SupportedInterpreters, request.ExecutionModes
	if request.Protocol == agentproto.AgentV1 {
		host = []agentproto.Interpreter{agentproto.InterpreterBash}
	}
	if request.Protocol != agentproto.AgentV3 {
		modes = []agentproto.ExecutionMode{agentproto.ExecutionHost}
	}
	return protocol == string(request.Protocol) &&
		sameCapabilities(host, snapshot.HostInterpreters) &&
		sameCapabilities(modes, snapshot.ExecutionModes) &&
		sameCapabilities(
			request.ContainerRuntimes,
			snapshot.ContainerRuntimes,
		) &&
		sameCapabilities(
			request.ContainerInterpreters,
			snapshot.ContainerInterpreters,
		)
}

func sameCapabilities[T ~string](reported []T, stored []string) bool {
	values := make([]string, len(reported))
	for i, value := range reported {
		values[i] = string(value)
	}
	slices.Sort(values)
	return slices.Equal(values, stored)
}

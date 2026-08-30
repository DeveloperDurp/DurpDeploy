package agentclient

import (
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/agenttls"
)

const legacyAgentVersion = agentproto.AgentVersion("legacy-unknown")

// NewPaired restores the identity and exact server pins established by pairing.
func NewPaired(
	stateDir string,
	agentVersion agentproto.AgentVersion,
) (*Client, error) {
	state, err := agentstate.NewStore(stateDir).Load()
	if err != nil {
		return nil, err
	}
	if agentVersion == "" {
		agentVersion = agentproto.AgentVersion(state.AgentVersion)
	}
	if agentVersion == "" {
		agentVersion = legacyAgentVersion
	}
	identity, err := agenttls.LoadExisting(stateDir)
	if err != nil {
		return nil, fmt.Errorf("load paired agent identity: %w", err)
	}
	client := &Client{
		serverURL:    state.ServerURL,
		stateDir:     stateDir,
		agentID:      agentproto.AgentID(state.AgentID),
		agentVersion: agentVersion,
		protocol:     agentproto.AgentV1,
		identity:     identity,
		pins:         append([]agenttls.Fingerprint(nil), state.ServerPins...),
		state:        state,
		now:          time.Now,
		sleep:        sleep,
		jitter:       randomInt,
	}
	tlsConfig, err := client.tlsConfig()
	if err != nil {
		return nil, err
	}
	client.http = &http.Client{
		Timeout: requestTimeout,
		Transport: &http.Transport{
			TLSClientConfig:     tlsConfig,
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        4,
			MaxConnsPerHost:     2,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		},
	}
	return client, nil
}

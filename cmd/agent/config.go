package main

import (
	"os"

	"durpdeploy/internal/agentclient"
	"durpdeploy/internal/agentproto"
)

type config struct {
	client agentclient.Config
}

func loadConfig() (config, error) {
	values := agentclient.Config{
		ServerURL:         os.Getenv("DURPDEPLOY_AGENT_SERVER_URL"),
		ServerFingerprint: os.Getenv("DURPDEPLOY_AGENT_SERVER_FINGERPRINT"),
		StateDir:          os.Getenv("DURPDEPLOY_AGENT_STATE_DIR"),
		EnrollmentToken:   os.Getenv("DURPDEPLOY_AGENT_ENROLLMENT_TOKEN"),
		AgentID:           agentproto.AgentID(os.Getenv("DURPDEPLOY_AGENT_ID")),
		Name:              os.Getenv("DURPDEPLOY_AGENT_NAME"),
		AgentVersion: agentproto.AgentVersion(
			os.Getenv("DURPDEPLOY_AGENT_VERSION"),
		),
		Protocol: string(agentproto.AgentV1),
	}
	return config{client: values}, nil
}

package main

import (
	"os"
	"path/filepath"

	"durpdeploy/internal/agentbootstrap"
	"durpdeploy/internal/agentproto"
)

type config struct {
	stateDir     string
	agentVersion agentproto.AgentVersion
	bootstrap    agentbootstrap.Config
}

func loadConfig() (config, error) {
	stateDir, err := stateDirectory()
	if err != nil {
		return config{}, err
	}
	return config{
		stateDir: stateDir,
		agentVersion: agentproto.AgentVersion(
			os.Getenv("DURPDEPLOY_AGENT_VERSION"),
		),
		bootstrap: agentbootstrap.Config{
			StateDir: stateDir,
			ListenAddr: os.Getenv(
				"DURPDEPLOY_AGENT_LISTEN_ADDR",
			),
		},
	}, nil
}

func stateDirectory() (string, error) {
	if directory := os.Getenv("DURPDEPLOY_AGENT_STATE_DIR"); directory != "" {
		return directory, nil
	}
	directory, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(directory, "durpdeploy-agent"), nil
}

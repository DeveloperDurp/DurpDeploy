package main

import (
	"strings"
	"testing"
)

func TestAgentHelp_listsOnlyLocalBootstrapInputs(t *testing.T) {
	// Given
	const forbidden = "DURPDEPLOY_AGENT_SERVER_URL"

	// When
	help := agentHelp()

	// Then
	for _, required := range []string{
		"DURPDEPLOY_AGENT_LISTEN_ADDR",
		"DURPDEPLOY_AGENT_STATE_DIR",
		"DURPDEPLOY_AGENT_VERSION",
	} {
		if !strings.Contains(help, required) {
			t.Fatalf("help does not mention %s: %s", required, help)
		}
	}
	if strings.Contains(help, forbidden) {
		t.Fatalf("help exposes manual server configuration: %s", help)
	}
}

func TestLoadConfig_usesDefaultStateDirectory(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_STATE_DIR", "")

	// When
	configuration, err := loadConfig()

	// Then
	if err != nil || configuration.stateDir == "" {
		t.Fatalf("default config = %#v, %v", configuration, err)
	}
}

func TestLoadConfig_ignoresManualServerConfiguration(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_SERVER_URL", "https://server.example.test")
	t.Setenv("DURPDEPLOY_AGENT_SERVER_FINGERPRINT", strings.Repeat("a", 64))
	t.Setenv("DURPDEPLOY_AGENT_ID", "manual-agent")
	t.Setenv("DURPDEPLOY_AGENT_NAME", "manual-agent")

	// When
	configuration, err := loadConfig()

	// Then
	if err != nil || configuration.stateDir == "" {
		t.Fatalf(
			"manual server configuration was retained: %#v, %v",
			configuration,
			err,
		)
	}
}

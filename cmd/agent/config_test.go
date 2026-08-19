package main

import (
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/agentclient"
)

func TestLoadConfig_allowsMissingEnrollmentTokenForRestart(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_ENROLLMENT_TOKEN", "")

	// When
	configuration, err := loadConfig()

	// Then
	if err != nil || configuration.client.EnrollmentToken != "" {
		t.Fatalf("restart config = %#v, %v", configuration, err)
	}
}

func TestLoadConfig_rejectsMissingServerPin(t *testing.T) {
	// Given
	t.Setenv("DURPDEPLOY_AGENT_ENROLLMENT_TOKEN", "token")
	t.Setenv("DURPDEPLOY_AGENT_SERVER_FINGERPRINT", "")
	configuration, err := loadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// When
	_, err = agentclient.New(configuration.client)

	// Then
	if err == nil {
		t.Fatal("missing server pin accepted")
	}
}

func TestLoadConfig_rejectsInsecureStateDirectory(t *testing.T) {
	// Given
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o755); err != nil {
		t.Fatalf("make state directory insecure: %v", err)
	}
	t.Setenv("DURPDEPLOY_AGENT_ENROLLMENT_TOKEN", "token")
	t.Setenv("DURPDEPLOY_AGENT_SERVER_URL", "https://127.0.0.1")
	t.Setenv("DURPDEPLOY_AGENT_SERVER_FINGERPRINT", strings.Repeat("a", 64))
	t.Setenv("DURPDEPLOY_AGENT_STATE_DIR", stateDir)
	t.Setenv("DURPDEPLOY_AGENT_ID", "agent")
	t.Setenv("DURPDEPLOY_AGENT_NAME", "agent")
	t.Setenv("DURPDEPLOY_AGENT_VERSION", "test")
	configuration, err := loadConfig()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	// When
	_, err = agentclient.New(configuration.client)

	// Then
	if err == nil {
		t.Fatal("insecure state directory accepted")
	}
}

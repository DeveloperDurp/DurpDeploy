package oidc

import (
	"errors"
	"strings"
	"testing"
)

func TestLoadConfigRejectsPartial(t *testing.T) {
	// Given: only a client secret from an incomplete OIDC configuration.
	env := map[string]string{
		"DURPDEPLOY_OIDC_CLIENT_SECRET": testClientSecret,
	}

	// When: startup parses the configuration.
	config, err := loadInvalidConfig(t, env)

	// Then: startup rejects it without exposing the secret.
	if config.Enabled {
		t.Fatal("LoadConfig() enabled partial configuration")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("LoadConfig() error = %v, want ErrInvalidConfig", err)
	}
	if strings.Contains(err.Error(), testClientSecret) {
		t.Fatal("LoadConfig() error exposes client secret")
	}
}

func TestLoadConfigRejectsDuplicateGroups(t *testing.T) {
	// Given: a complete configuration whose role groups overlap.
	env := completeEnv()
	env["DURPDEPLOY_OIDC_ADMIN_GROUP"] = "operators"
	env["DURPDEPLOY_OIDC_DEPLOYER_GROUP"] = "operators"

	// When: startup parses the configuration.
	config, err := loadInvalidConfig(t, env)

	// Then: startup rejects ambiguous role mapping.
	if config.Enabled {
		t.Fatal("LoadConfig() enabled duplicate role groups")
	}
	if !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("LoadConfig() error = %v, want ErrInvalidConfig", err)
	}
}

func loadInvalidConfig(t *testing.T, env map[string]string) (Config, error) {
	t.Helper()
	clearOIDCEnv(t)
	for key, value := range env {
		t.Setenv(key, value)
	}
	return LoadConfig()
}

package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStartupDefaultStepTimeout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	require.NoError(t, os.WriteFile(
		path,
		[]byte(
			"limits:\n  execution:\n    default_timeout_seconds: 600\n",
		),
		0600,
	))
	values := map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
	}
	startup, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(
		t,
		int64(600),
		startup.settings.Limits.Execution.DefaultTimeoutSeconds,
	)
	values["DURPDEPLOY_LIMITS_EXECUTION_DEFAULT_TIMEOUT_SECONDS"] = "2"
	override, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(
		t,
		int64(2),
		override.settings.Limits.Execution.DefaultTimeoutSeconds,
	)
	require.Equal(
		t,
		int64(600),
		startup.settings.Limits.Execution.DefaultTimeoutSeconds,
	)
	for _, value := range []string{"0", "-1", "86401", "9223372036854775807"} {
		values["DURPDEPLOY_LIMITS_EXECUTION_DEFAULT_TIMEOUT_SECONDS"] = value
		_, err := loadServerStartupWithLookup(startupLookup(values))
		require.Error(t, err)
	}
	values["DURPDEPLOY_LIMITS_EXECUTION_DEFAULT_TIMEOUT_SECONDS"] = "2"
	for key, value := range map[string]string{
		"DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES":         "11",
		"DURPDEPLOY_LIMITS_EXECUTION_MAX_TIMEOUT_SECONDS": "86401",
	} {
		values[key] = value
		_, err := loadServerStartupWithLookup(startupLookup(values))
		require.NoError(t, err)
		delete(values, key)
	}
}

func TestStartupExecutionMaxima(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte(
				"limits:\n  execution:\n    max_retries: 11\n    max_timeout_seconds: 172800\n",
			),
			0600,
		),
	)
	values := map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
	}
	startup, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(t, int64(11), startup.settings.Limits.Execution.MaxRetries)
	require.Equal(
		t,
		int64(172800),
		startup.settings.Limits.Execution.MaxTimeoutSeconds,
	)
	values["DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES"] = "0"
	values["DURPDEPLOY_LIMITS_EXECUTION_MAX_TIMEOUT_SECONDS"] = "3"
	_, err = loadServerStartupWithLookup(startupLookup(values))
	require.ErrorContains(t, err, "default timeout exceeds maximum timeout")
	values["DURPDEPLOY_LIMITS_EXECUTION_DEFAULT_TIMEOUT_SECONDS"] = "3"
	override, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(
		t,
		config.ExecutionLimits{
			MaxRetries:            0,
			MaxTimeoutSeconds:     3,
			DefaultTimeoutSeconds: 3,
		},
		override.settings.Limits.Execution,
	)
	require.Equal(t, int64(11), startup.settings.Limits.Execution.MaxRetries)
	for key, value := range map[string]string{
		"DURPDEPLOY_LIMITS_EXECUTION_MAX_RETRIES":         "-1",
		"DURPDEPLOY_LIMITS_EXECUTION_MAX_TIMEOUT_SECONDS": "9223372036854775807",
	} {
		before := values[key]
		values[key] = value
		_, err := loadServerStartupWithLookup(startupLookup(values))
		require.Error(t, err)
		values[key] = before
	}
}

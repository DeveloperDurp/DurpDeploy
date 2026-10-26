package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStartupArtifactTimeoutPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte(
				"limits:\n  artifact:\n    fetch_timeout_seconds: 9\n    dial_timeout_seconds: 8\n    tls_timeout_seconds: 7\n    header_timeout_seconds: 6\n",
			),
			0600,
		),
	)
	values := map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_LIMITS_ARTIFACT_FETCH_TIMEOUT_SECONDS": "5",
	}
	startup, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	limits := startup.settings.Limits.Artifact
	require.Equal(t, int64(5), limits.FetchTimeoutSeconds)
	require.Equal(t, int64(8), limits.DialTimeoutSeconds)
	require.Equal(t, int64(7), limits.TLSTimeoutSeconds)
	require.Equal(t, int64(6), limits.HeaderTimeoutSeconds)
	values["DURPDEPLOY_LIMITS_ARTIFACT_FETCH_TIMEOUT_SECONDS"] = "0"
	_, err = loadServerStartupWithLookup(startupLookup(values))
	require.Error(t, err)
	require.Equal(
		t,
		int64(5),
		startup.settings.Limits.Artifact.FetchTimeoutSeconds,
	)
	delete(values, "DURPDEPLOY_LIMITS_ARTIFACT_FETCH_TIMEOUT_SECONDS")
	values["DURPDEPLOY_LIMITS_STAGING_PAYLOAD_BYTES"] = "1024"
	_, err = loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
}

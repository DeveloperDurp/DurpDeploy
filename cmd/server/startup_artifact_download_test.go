package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStartupArtifactDownloadPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte("limits:\n  artifact:\n    download_bytes: 4096\n"),
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
	require.Equal(
		t,
		int64(4096),
		startup.settings.Limits.Artifact.DownloadBytes,
	)
	values["DURPDEPLOY_LIMITS_ARTIFACT_DOWNLOAD_BYTES"] = "629145600"
	startup, err = loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(
		t,
		int64(629145600),
		startup.settings.Limits.Artifact.DownloadBytes,
	)
	values["DURPDEPLOY_LIMITS_ARTIFACT_DOWNLOAD_BYTES"] = "0"
	_, err = loadServerStartupWithLookup(startupLookup(values))
	require.Error(t, err)
	require.Equal(
		t,
		int64(629145600),
		startup.settings.Limits.Artifact.DownloadBytes,
	)
}

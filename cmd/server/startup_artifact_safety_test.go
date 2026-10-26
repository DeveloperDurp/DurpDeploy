package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStartupArtifactSafetyPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte(
				"limits:\n  artifact:\n    extracted_bytes: 1024\n    files: 3\n    staging_nodes: 8\n    metadata_bytes: 4096\n",
			),
			0600,
		),
	)
	values := map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_LIMITS_ARTIFACT_FILES": "4",
	}
	s, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(t, int64(1024), s.settings.Limits.Artifact.ExtractedBytes)
	require.Equal(t, int64(4), s.settings.Limits.Artifact.Files)
	require.Equal(t, int64(8), s.settings.Limits.Artifact.StagingNodes)
	require.Equal(t, int64(4096), s.settings.Limits.Artifact.MetadataBytes)
	values["DURPDEPLOY_LIMITS_ARTIFACT_STAGING_NODES"] = "3"
	_, err = loadServerStartupWithLookup(startupLookup(values))
	require.Error(t, err)
	require.Equal(t, int64(8), s.settings.Limits.Artifact.StagingNodes)
}

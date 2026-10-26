package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStartupStagingArchivePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte(
				"limits:\n  staging:\n    payload_bytes: 24\n    entries: 3\n    archive_bytes: 4096\n",
			),
			0600,
		),
	)
	values := map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_LIMITS_STAGING_ARCHIVE_BYTES": "570426368",
	}
	s, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(t, int64(570426368), s.settings.Limits.Staging.ArchiveBytes)
	delete(values, "DURPDEPLOY_LIMITS_STAGING_ARCHIVE_BYTES")
	s, err = loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(t, int64(4096), s.settings.Limits.Staging.ArchiveBytes)
	values["DURPDEPLOY_LIMITS_STAGING_ARCHIVE_BYTES"] = "1047"
	_, err = loadServerStartupWithLookup(startupLookup(values))
	require.ErrorContains(
		t,
		err,
		"staging archive must allow payload and TAR framing",
	)
}

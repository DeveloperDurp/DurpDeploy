package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStartupTransferPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte(
				"limits:\n  transfer:\n    global: 6\n    per_agent: 3\n    idle_timeout_seconds: 2\n",
			),
			0o600,
		),
	)
	values := map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_LIMITS_TRANSFER_GLOBAL": "8",
	}
	startup, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(
		t,
		config.TransferLimits{Global: 8, PerAgent: 3, IdleTimeoutSeconds: 2},
		startup.settings.Limits.Transfer,
	)
	values["DURPDEPLOY_LIMITS_TRANSFER_GLOBAL"] = "1"
	require.Equal(t, int64(8), startup.settings.Limits.Transfer.Global)
}

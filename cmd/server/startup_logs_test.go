package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStartupLogPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	require.NoError(t, os.WriteFile(path, []byte(`limits:
  logs:
    page_bytes: 8192
    line_bytes: 256
    preview_batch: 2
    pending_bytes: 128
    export_batch: 3
    page_rows: 4
`), 0600))
	values := map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_LIMITS_LOGS_PAGE_BYTES":    "2097152",
		"DURPDEPLOY_LIMITS_LOGS_LINE_BYTES":    "32768",
		"DURPDEPLOY_LIMITS_LOGS_PREVIEW_BATCH": "65",
		"DURPDEPLOY_LIMITS_LOGS_PENDING_BYTES": "1048577",
		"DURPDEPLOY_LIMITS_LOGS_EXPORT_BATCH":  "257",
		"DURPDEPLOY_LIMITS_LOGS_PAGE_ROWS":     "501",
	}
	startup, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	want := config.LogLimits{PageBytes: 2097152, LineBytes: 32768,
		PreviewBatch: 65, PendingBytes: 1048577, ExportBatch: 257, PageRows: 501}
	require.Equal(t, want, startup.settings.Limits.Logs)
	for key := range values {
		if key != config.FileEnv && key != "DURPDEPLOY_SECRET_KEY" {
			delete(values, key)
		}
	}
	require.Equal(
		t,
		want,
		startup.settings.Limits.Logs,
		"captured policy changed",
	)
	startup, err = loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(t, config.LogLimits{PageBytes: 8192, LineBytes: 256,
		PreviewBatch: 2, PendingBytes: 128, ExportBatch: 3, PageRows: 4}, startup.settings.Limits.Logs)
}

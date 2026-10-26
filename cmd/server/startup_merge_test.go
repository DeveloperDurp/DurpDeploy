package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestStartupMergePolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "startup.yaml")
	require.NoError(
		t,
		os.WriteFile(
			path,
			[]byte(
				"limits:\n  merge:\n    archive_bytes: 4096\n    entry_visits: 4\n    agent_outputs: 2\n",
			),
			0600,
		),
	)
	values := map[string]string{
		config.FileEnv: path,
		"DURPDEPLOY_SECRET_KEY": base64.StdEncoding.EncodeToString(
			make([]byte, 32),
		),
		"DURPDEPLOY_LIMITS_MERGE_ARCHIVE_BYTES": "2147483649",
		"DURPDEPLOY_LIMITS_MERGE_ENTRY_VISITS":  "200001",
		"DURPDEPLOY_LIMITS_MERGE_AGENT_OUTPUTS": "65",
	}
	s, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(
		t,
		config.MergeLimits{
			ArchiveBytes: 2147483649,
			EntryVisits:  200001,
			AgentOutputs: 65,
		},
		s.settings.Limits.Merge,
	)
	for _, field := range []string{"ARCHIVE_BYTES", "ENTRY_VISITS", "AGENT_OUTPUTS"} {
		delete(values, "DURPDEPLOY_LIMITS_MERGE_"+field)
	}
	yaml, err := loadServerStartupWithLookup(startupLookup(values))
	require.NoError(t, err)
	require.Equal(
		t,
		config.MergeLimits{ArchiveBytes: 4096, EntryVisits: 4, AgentOutputs: 2},
		yaml.settings.Limits.Merge,
	)
	require.Equal(t, int64(65), s.settings.Limits.Merge.AgentOutputs)
	for _, field := range []string{"ARCHIVE_BYTES", "ENTRY_VISITS", "AGENT_OUTPUTS"} {
		values["DURPDEPLOY_LIMITS_MERGE_"+field] = "0"
		_, err := loadServerStartupWithLookup(startupLookup(values))
		require.Error(t, err)
		delete(values, "DURPDEPLOY_LIMITS_MERGE_"+field)
	}
}

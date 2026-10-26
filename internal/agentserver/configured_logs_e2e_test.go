//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredLogsRemoteAPIE2E(t *testing.T) {
	for key, value := range map[string]string{
		"PAGE_BYTES": "8192", "LINE_BYTES": "256", "PREVIEW_BATCH": "2",
		"PENDING_BYTES": "4096", "EXPORT_BATCH": "3", "PAGE_ROWS": "4",
	} {
		t.Setenv("DURPDEPLOY_LIMITS_LOGS_"+key, value)
	}
	merge := config.Defaults().Limits.Merge
	s := newStagingE2EConfigured(t, stagingFixtureConfig{
		serverContainers: true, mergeLimits: &merge,
	})
	var effective config.Limits
	require.NoError(
		t,
		json.Unmarshal(
			s.api(t, "GET", "/admin/resource-config", nil, 200),
			&effective,
		),
	)
	require.Equal(t, config.LogLimits{PageBytes: 8192, LineBytes: 256,
		PreviewBatch: 2, PendingBytes: 4096, ExportBatch: 3, PageRows: 4}, effective.Logs)
	deployment := configuredMergeDeployment(t, s)
	s.wait(t, deployment, "succeeded")
	logs := string(
		s.api(
			t,
			"GET",
			fmt.Sprintf("/deployments/%d/logs.txt", deployment),
			nil,
			200,
		),
	)
	require.Contains(t, logs, "merged-downstream-ok")
	s.assertTransferPurged(t, "configured-logs", deployment)
	s.assertRuntimeStagingPurged(t)
}

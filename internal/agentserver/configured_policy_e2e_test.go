//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredSharedPolicyRemoteAPIE2E(t *testing.T) {
	for key, value := range map[string]string{
		"EXECUTION_MAX_RETRIES": "11", "EXECUTION_MAX_TIMEOUT_SECONDS": "90000",
		"EXECUTION_DEFAULT_TIMEOUT_SECONDS": "30",
		"CONTAINER_PIDS":                    "96", "CONTAINER_HELPER_PIDS": "48",
		"CONTAINER_TMPFS_BYTES": "33554432", "CONTAINER_HELPER_CPUS": "0.5",
		"CONTAINER_HELPER_MEMORY_OVERHEAD_BYTES": "67108864",
		"STAGING_PAYLOAD_BYTES":                  "1048576", "STAGING_ENTRIES": "128",
		"STAGING_ARCHIVE_BYTES":   "2097152",
		"ARTIFACT_DOWNLOAD_BYTES": "1048576", "ARTIFACT_EXTRACTED_BYTES": "1048576",
		"ARTIFACT_FILES": "64", "ARTIFACT_STAGING_NODES": "127",
		"ARTIFACT_METADATA_BYTES": "262144", "ARTIFACT_FETCH_TIMEOUT_SECONDS": "20",
		"ARTIFACT_DIAL_TIMEOUT_SECONDS": "5", "ARTIFACT_TLS_TIMEOUT_SECONDS": "5",
		"ARTIFACT_HEADER_TIMEOUT_SECONDS": "5", "TRANSFER_IDLE_TIMEOUT_SECONDS": "10",
	} {
		t.Setenv("DURPDEPLOY_LIMITS_"+key, value)
		t.Setenv("DURPDEPLOY_AGENT_LIMITS_"+key, value)
	}
	merge := config.Defaults().Limits.Merge
	s := newStagingE2EConfigured(t, stagingFixtureConfig{
		serverContainers: true, mergeLimits: &merge,
	})
	var effective config.Limits
	require.NoError(t, json.Unmarshal(
		s.api(t, "GET", "/admin/resource-config", nil, 200), &effective,
	))
	require.EqualValues(t, 11, effective.Execution.MaxRetries)
	require.EqualValues(t, 96, effective.Container.PIDs)
	advertisement, err := effective.AgentPolicy().Advertisement()
	require.NoError(t, err)
	for _, id := range []string{"test-agent", "second-agent"} {
		stored, err := s.f.repo.Queries.GetAgentResourcePolicy(t.Context(), id)
		require.NoError(t, err)
		require.Equal(t, advertisement, stored)
	}
	for _, scenario := range []string{"agent-first", "failure", "cancel"} {
		t.Run(
			scenario,
			func(t *testing.T) { s.checkPackageScenario(t, scenario) },
		)
	}
	deployment := configuredMergeDeployment(t, s)
	s.wait(t, deployment, "succeeded")
	s.assertTransferPurged(t, "custom-policy-fanout", deployment)
	s.assertRuntimeStagingPurged(t)
	testRaisedPolicyContainer(t, s)
}

func testRaisedPolicyContainer(t *testing.T, s *stagingE2E) {
	t.Helper()
	base, environment := s.packageProject(t, "raised-policy")
	step := map[string]any{
		"name": "Custom policy", "execution_target": "agent",
		"agent_execution_mode": "container", "agent_selectors": []string{"test-agent"},
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"timeout_seconds": 90000, "max_retries": 12,
		"script_body": `set -eu
test "$(cat /sys/fs/cgroup/pids.max)" = 96
echo custom-policy-container-ok`,
	}
	s.api(t, "POST", base+"/steps", step, 400)
	step["max_retries"] = 11
	s.api(t, "POST", base+"/steps", step, 201)
	var release, deployment struct{ ID int64 }
	require.NoError(t, json.Unmarshal(s.api(t, "POST", base+"/releases",
		map[string]string{"version": "raised"}, 201), &release))
	require.NoError(t, json.Unmarshal(s.api(
		t,
		"POST",
		base+"/deployments",
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": environment,
		},
		201,
	), &deployment))
	s.wait(t, deployment.ID, "succeeded")
	logs := s.api(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID),
		nil,
		200,
	)
	require.Contains(t, string(logs), "custom-policy-container-ok")
	s.assertTransferPurged(t, "raised-policy", deployment.ID)
	s.assertRuntimeStagingPurged(t)
}

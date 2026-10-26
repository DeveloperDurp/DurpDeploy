//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/config"
	"github.com/stretchr/testify/require"
)

func TestConfiguredStagingMergeAPIE2E(t *testing.T) {
	for _, field := range []string{"bytes", "entries", "outputs"} {
		for _, accepted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", field, accepted), func(t *testing.T) {
				testConfiguredStagingMerge(t, field, accepted)
			})
		}
	}
}

func testConfiguredStagingMerge(t *testing.T, field string, accepted bool) {
	t.Helper()
	budget := config.MergeLimits{
		ArchiveBytes: 5120,
		EntryVisits:  2,
		AgentOutputs: 2,
	}
	if !accepted {
		switch field {
		case "bytes":
			budget.ArchiveBytes--
		case "entries":
			budget.EntryVisits--
		case "outputs":
			budget.AgentOutputs--
		}
	}
	s := newStagingE2EConfigured(
		t,
		stagingFixtureConfig{
			serverContainers: true,
			mergeLimits:      &budget,
		},
	)
	// Later process environment changes cannot replace captured limits.
	for _, field := range []string{"ARCHIVE_BYTES", "ENTRY_VISITS", "AGENT_OUTPUTS"} {
		t.Setenv("DURPDEPLOY_LIMITS_MERGE_"+field, "1")
	}
	var limits config.Limits
	require.NoError(
		t,
		json.Unmarshal(
			s.api(t, "GET", "/admin/resource-config", nil, 200),
			&limits,
		),
	)
	require.Equal(t, budget, limits.Merge)
	deployment := configuredMergeDeployment(t, s)
	if accepted {
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
		s.assertTransferPurged(t, field, deployment)
		s.assertRuntimeStagingPurged(t)
	} else {
		s.assertMergeBudgetRejected(t, field, deployment)
		var attempts int
		require.NoError(t, s.f.repo.DB.QueryRowContext(t.Context(), "SELECT count(*) FROM deployment_logs WHERE deployment_id=? AND step_index=1 AND line='Step running.'", deployment).Scan(&attempts))
		require.Zero(t, attempts, "rejected merge started the downstream step")
	}
}

func configuredMergeDeployment(t *testing.T, s *stagingE2E) int64 {
	t.Helper()
	var project, environment, release, deployment struct{ ID int64 }
	decode := func(data []byte, into any) {
		t.Helper()
		require.NoError(t, json.Unmarshal(data, into))
	}
	decode(
		s.api(
			t,
			"POST",
			"/projects",
			map[string]string{"name": "Configured merge"},
			201,
		),
		&project,
	)
	decode(
		s.api(
			t,
			"POST",
			"/environments",
			map[string]string{"name": "Configured merge"},
			201,
		),
		&environment,
	)
	for _, id := range []string{"test-agent", "second-agent"} {
		s.api(
			t,
			"POST",
			"/admin/agents/"+id+"/environments",
			map[string]int64{"environment_id": environment.ID},
			204,
		)
	}
	base := fmt.Sprintf("/projects/%d", project.ID)
	s.api(t, "POST", base+"/steps", map[string]any{
		"name": "Merge producer", "script_body": `printf merged > "$DURPDEPLOY_STAGE_DIR/result"`,
		"execution_target": "agent", "agent_execution_mode": "host", "interpreter": "bash",
	}, 201)
	s.api(t, "POST", base+"/steps", map[string]any{
		"name": "Merge consumer", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"script_body": `set -eu
test "$(cat "$DURPDEPLOY_STAGE_DIR/result")" = merged
echo merged-downstream-ok`,
	}, 201)
	decode(
		s.api(
			t,
			"POST",
			base+"/releases",
			map[string]string{"version": "merge-1"},
			201,
		),
		&release,
	)
	decode(
		s.api(
			t,
			"POST",
			base+"/deployments",
			map[string]int64{
				"release_id":     release.ID,
				"environment_id": environment.ID,
			},
			201,
		),
		&deployment,
	)
	return deployment.ID
}

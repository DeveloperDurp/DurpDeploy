//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/config"
	"durpdeploy/internal/dispatch"
	"github.com/DeveloperDurp/durpdeploy-agent/resourcepolicy"
	"github.com/stretchr/testify/require"
)

func TestIndependentAgentLimitsAPIE2E(t *testing.T) {
	t.Setenv("DURPDEPLOY_LIMITS_CONTAINER_TMPFS_BYTES", "381681664")
	merge := config.Defaults().Limits.Merge
	wrapper, gate := stagingCleanupGate(t)
	s := newStagingE2EConfigured(t, stagingFixtureConfig{
		serverContainers: true,
		mergeLimits:      &merge,
		runtimeBinary:    wrapper,
		agentEnv: map[string][]string{
			"test-agent": {
				"DURPDEPLOY_AGENT_LIMITS_CONTAINER_TMPFS_BYTES=4194304",
				"DURPDEPLOY_AGENT_LIMITS_EXECUTION_MAX_RETRIES=0",
			},
			"second-agent": {
				"DURPDEPLOY_AGENT_LIMITS_CONTAINER_TMPFS_BYTES=536870912",
			},
		},
	})
	for _, id := range []string{"test-agent", "second-agent"} {
		s.api(t, http.MethodPost, "/admin/agents/"+id+"/labels",
			map[string]string{"label": "scratch-agents"}, 204)
	}
	const measure = `df -Pk /tmp | awk 'NR == 2 { print "scratch-kib=" $2 }'`
	local := independentContainerStep("Server scratch", measure)
	remote := independentAgentStep("Agent scratch", measure, "scratch-agents")
	id := s.independentLimitsDeployment(t, "mixed-scratch", local, remote)
	s.wait(t, id, "succeeded")
	logs := s.independentLimitsLogs(t, id)
	for _, expected := range []string{
		"scratch-kib=372736", "scratch-kib=4096", "scratch-kib=524288",
	} {
		require.Contains(t, logs, expected)
	}
	s.assertTransferPurged(t, "mixed-scratch", id)
	s.assertRuntimeStagingPurged(t)

	t.Run("scratch exhaustion", func(t *testing.T) {
		step := independentAgentStep("Fill scratch", `set -eu
dd if=/dev/zero of=/tmp/fill bs=1M count=5
echo unexpected-write`, "test-agent")
		id := s.independentLimitsDeployment(t, "scratch-exhaustion", step)
		s.wait(t, id, "failed")
		logs := s.independentLimitsLogs(t, id)
		require.Contains(t, logs, "No space left on device")
		require.NotContains(t, logs, "unexpected-write")
		s.assertTransferPurged(t, "scratch-exhaustion", id)
		s.assertRuntimeStagingPurged(t)
	})

	t.Run("agent rejects claim locally", func(t *testing.T) {
		s.checkLocalAgentRejection(t, gate)
	})
	t.Run("deadline before claim expiry", func(t *testing.T) {
		s.checkUnstartedAgentDeadline(t)
	})
}

func (s *stagingE2E) checkUnstartedAgentDeadline(t *testing.T) {
	t.Helper()
	countRejections := func() int {
		logs, err := os.ReadFile(s.agentLogs["test-agent"])
		require.NoError(t, err)
		return strings.Count(string(logs), "max retries must not exceed 0")
	}
	before := countRejections()
	step := independentAgentStep(
		"Short deadline", "echo unexpected-execution", "test-agent",
	)
	step["max_retries"] = 1
	step["timeout_seconds"] = 5
	id := s.independentLimitsDeployment(t, "short-agent-deadline", step)
	s.waitFor(t, id, "failed", 20*time.Second, 100*time.Millisecond)
	require.Eventually(t, func() bool {
		return !s.f.broker.DeploymentActive(id)
	}, 5*time.Second, 50*time.Millisecond)
	logs := s.independentLimitsLogs(t, id)
	require.Contains(t, logs, "Step failed.")
	require.NotContains(t, logs, "unexpected-execution")
	require.Greater(t, countRejections(), before)
	var state string
	require.NoError(t, s.f.repo.DB.QueryRowContext(t.Context(),
		"SELECT state FROM remote_step_runs WHERE deployment_id=?", id,
	).Scan(&state))
	require.Equal(t, "cancelled", state)
	s.assertTransferPurged(t, "short-agent-deadline", id)
}

func TestLegacyAgentWithoutResourcePolicyAPIE2E(t *testing.T) {
	t.Setenv("DURPDEPLOY_LIMITS_CONTAINER_TMPFS_BYTES", "381681664")
	merge := config.Defaults().Limits.Merge
	s := newStagingE2EConfigured(t, stagingFixtureConfig{
		mergeLimits: &merge,
		wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					r.Header.Del(resourcepolicy.CapabilityHeader)
					next.ServeHTTP(w, r)
				},
			)
		},
	})
	step := map[string]any{
		"name": "Legacy host step", "script_body": "echo legacy-host-completed",
		"execution_target": "agent", "agent_selectors": []string{"test-agent"},
	}
	id := s.independentLimitsDeployment(t, "legacy-host", step)
	s.wait(t, id, "succeeded")
	require.Contains(t, s.independentLimitsLogs(t, id), "legacy-host-completed")
	s.assertTransferPurged(t, "legacy-host", id)
}

func stagingCleanupGate(t *testing.T) (string, string) {
	t.Helper()
	runtime, err := exec.LookPath("podman")
	require.NoError(t, err)
	gate := t.TempDir()
	t.Setenv("DURPDEPLOY_TEST_CLEANUP_GATE", gate)
	t.Setenv("DURPDEPLOY_TEST_CLEANUP_RUNTIME", runtime)
	wrapper := filepath.Join(gate, "podman")
	const script = `#!/bin/sh
gate="$DURPDEPLOY_TEST_CLEANUP_GATE"
case "$*" in
    *"volume rm durpdeploy-artifact-"*)
        if test -e "$gate/armed"; then
            touch "$gate/entered"
            while ! test -e "$gate/release"; do sleep 0.05; done
        fi
        ;;
esac
exec "$DURPDEPLOY_TEST_CLEANUP_RUNTIME" "$@"
`
	require.NoError(t, os.WriteFile(wrapper, []byte(script), 0700))
	return wrapper, gate
}

func (s *stagingE2E) checkLocalAgentRejection(t *testing.T, gate string) {
	t.Helper()
	step := independentAgentStep(
		"Agent retry ceiling", "echo unexpected-execution", "test-agent",
	)
	step["max_retries"] = 1
	local := independentContainerStep(
		"Seed local staging", `printf seed > "$DURPDEPLOY_STAGE_DIR/marker"`,
	)
	id := s.independentLimitsDeployment(t, "agent-retry-ceiling", local, step)
	releaseGate := func() {
		if err := os.WriteFile(filepath.Join(gate, "release"), nil, 0600); err != nil {
			t.Error(err)
		}
	}
	defer releaseGate()
	var queued int64
	for attempt := range 2 {
		require.Eventually(t, func() bool {
			logs, err := os.ReadFile(s.agentLogs["test-agent"])
			return err == nil &&
				strings.Count(
					string(logs),
					"max retries must not exceed 0",
				) > attempt
		}, 15*time.Second, 100*time.Millisecond)
		if attempt == 1 {
			queued = s.deployBehindRejectedClaim(t, id)
			require.NoError(
				t,
				os.WriteFile(filepath.Join(gate, "armed"), nil, 0600),
			)
		}
		// Advance this lease to its deadline without waiting a real minute.
		result, err := s.f.repo.DB.ExecContext(t.Context(), `
UPDATE remote_step_runs SET claim_expires_at=0
WHERE deployment_id=? AND state='claimed' AND started_at IS NULL`, id)
		require.NoError(t, err)
		changed, err := result.RowsAffected()
		require.NoError(t, err)
		require.EqualValues(t, 1, changed)
		require.NoError(t, dispatch.New(s.f.repo).Maintain(t.Context()))
	}
	require.Eventually(t, func() bool {
		_, err := os.Stat(filepath.Join(gate, "entered"))
		return err == nil
	}, 10*time.Second, 50*time.Millisecond)
	s.wait(t, id, "running")
	s.wait(t, queued, "queued")
	s.api(
		t,
		http.MethodPost,
		fmt.Sprintf("/deployments/%d/cancel", queued),
		nil,
		200,
	)
	s.wait(t, queued, "cancelled")
	releaseGate()
	s.wait(t, id, "failed")
	require.Eventually(t, func() bool {
		return !s.f.broker.DeploymentActive(id)
	}, 5*time.Second, 50*time.Millisecond)
	logs := s.independentLimitsLogs(t, id)
	require.Contains(t, logs, "did not start the remote step after two claims")
	require.NotContains(t, logs, "unexpected-execution")
	s.assertTransferPurged(t, "agent-retry-ceiling", id)
	s.assertRuntimeStagingPurged(t)
}

func (s *stagingE2E) deployBehindRejectedClaim(t *testing.T, id int64) int64 {
	t.Helper()
	deployment, err := s.f.repo.Queries.GetDeployment(t.Context(), id)
	require.NoError(t, err)
	release, err := s.f.repo.Queries.GetRelease(
		t.Context(),
		deployment.ReleaseID,
	)
	require.NoError(t, err)
	var queued struct{ ID int64 }
	require.NoError(t, json.Unmarshal(s.api(t, http.MethodPost,
		fmt.Sprintf("/projects/%d/deployments", release.ProjectID),
		map[string]int64{
			"release_id": release.ID, "environment_id": deployment.EnvironmentID,
		}, 201,
	), &queued))
	s.wait(t, queued.ID, "queued")
	return queued.ID
}

func independentContainerStep(name, script string) map[string]any {
	return map[string]any{
		"name": name, "script_body": script, "execution_target": "local",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}
}

func independentAgentStep(name, script, label string) map[string]any {
	step := independentContainerStep(name, script)
	step["execution_target"] = "agent"
	step["agent_execution_mode"] = "container"
	step["agent_selectors"] = []string{label}
	return step
}

func (s *stagingE2E) independentLimitsDeployment(
	t *testing.T, name string, steps ...map[string]any,
) int64 {
	t.Helper()
	base, environment := s.packageProject(t, name)
	for _, step := range steps {
		s.api(t, http.MethodPost, base+"/steps", step, 201)
	}
	var release, deployment struct{ ID int64 }
	require.NoError(t, json.Unmarshal(s.api(t, http.MethodPost,
		base+"/releases", map[string]string{"version": name}, 201,
	), &release))
	require.NoError(t, json.Unmarshal(s.api(t, http.MethodPost,
		base+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": environment,
		}, 201,
	), &deployment))
	return deployment.ID
}

func (s *stagingE2E) independentLimitsLogs(t *testing.T, id int64) string {
	t.Helper()
	return string(s.api(t, http.MethodGet,
		fmt.Sprintf("/deployments/%d/logs.txt", id), nil, 200,
	))
}

//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestDeploymentStagingPackageBashVerificationAPIE2E(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprintf("server-containers-%t", enabled), func(t *testing.T) {
			checkPackageBashVerification(t, enabled)
		})
	}
}

func checkPackageBashVerification(t *testing.T, enabled bool) {
	t.Helper()
	// Given agent-only release steps and frozen local Bash verification.
	s := newStagingE2EConfigured(
		t,
		stagingFixtureConfig{serverContainers: enabled},
	)
	base, environmentID := s.packageProject(t, "bash-verification")
	workspace := s.packageSource(t, packageSourceConfig{base: base})
	s.api(
		t,
		"PUT",
		fmt.Sprintf("/environments/%d", environmentID),
		map[string]any{
			"name":                         "package-bash-verification",
			"verification_type":            "bash",
			"verification_target":          `set -eu; test "$(cat "$ARTIFACT_PATH/package.txt")" = package; ! touch "$ARTIFACT_PATH/write"; awk '$5 == "/artifacts" { if ($6 !~ /ro/ || $6 !~ /noexec/) exit 1; found=1 } END { exit !found }' /proc/self/mountinfo; echo verification-package-complete`,
			"verification_timeout_seconds": 20,
		},
		200,
	)
	s.api(t, "POST", base+"/steps", map[string]any{
		"name":                 "agent package",
		"execution_target":     "agent",
		"agent_execution_mode": "host",
		"agent_selectors":      []string{"test-agent"},
		"script_body":          `set -eu; test "$(cat "$ARTIFACT_PATH/package.txt")" = package; echo remote-package-complete`,
	}, 201)
	var release, deployment struct{ ID int64 }
	if err := json.Unmarshal(
		s.api(
			t,
			"POST",
			base+"/releases",
			map[string]string{"version": "v1"},
			201,
		),
		&release,
	); err != nil {
		t.Fatal(err)
	}

	// When the public API starts the deployment snapshot.
	if err := json.Unmarshal(
		s.api(t, "POST", base+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": environmentID,
		}, 201),
		&deployment,
	); err != nil {
		t.Fatal(err)
	}

	// Then verification receives its package, or missing local execution fails first.
	want := "failed"
	if enabled {
		want = "succeeded"
	}
	s.wait(t, deployment.ID, want)
	logs := string(
		s.api(
			t,
			"GET",
			fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID),
			nil,
			200,
		),
	)
	if enabled {
		if !strings.Contains(logs, "verification-package-complete") {
			t.Fatalf("package verification did not execute: %s", logs)
		}
	} else if strings.Contains(logs, "remote-package-complete") || !strings.Contains(logs, "Artifact staging failed") {
		t.Fatalf("remote work ran before local runtime rejection: %s", logs)
	}
	s.assertTransferPurged(t, "bash-verification", deployment.ID)
	s.assertRuntimeStagingPurged(t)
	files, err := os.ReadDir(workspace)
	if err != nil || len(files) != 1 || files[0].Name() != ".lock" {
		t.Fatalf("package files retained: %v %v", files, err)
	}
}

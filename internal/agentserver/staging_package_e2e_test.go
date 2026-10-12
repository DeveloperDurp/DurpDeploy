//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestDeploymentStagingPackageAPIE2E(t *testing.T) {
	s := newStagingE2E(t)
	for _, scenario := range []string{"handoff", "agent-first", "identical", "conflict", "failure", "cancel", "changed-package", "slow-package"} {
		t.Run(scenario, func(t *testing.T) {
			base, environmentID := s.packageProject(t, scenario)
			workspace := s.packageSource(
				t,
				packageSourceConfig{base: base, scenario: scenario},
			)
			add := func(target, mode, selector, script string) {
				t.Helper()
				step := map[string]any{
					"name":             "package",
					"execution_target": target,
					"interpreter":      "bash",
					"timeout_seconds":  120,
					"script_body":      "set -eu\ntest \"$(cat \"$ARTIFACT_PATH/package.txt\")\" = package\n" + script,
				}
				if target == "local" || mode == "container" {
					step["container_image"] = "docker.io/library/bash:5.2"
					step["script_body"] = step["script_body"].(string) + "\n! touch \"$ARTIFACT_PATH/write\"\nawk '$5 == \"/artifacts\" { if ($6 !~ /ro/ || $6 !~ /noexec/) exit 1; found=1 } END { exit !found }' /proc/self/mountinfo"
				}
				if target == "agent" {
					step["agent_execution_mode"] = mode
				}
				if selector != "" {
					step["agent_selectors"] = []string{selector}
				}
				if scenario == "slow-package" && target == "agent" {
					step["timeout_seconds"] = 2
				}
				s.api(t, "POST", base+"/steps", step, 201)
			}
			if scenario == "handoff" {
				add(
					"local",
					"",
					"",
					`test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-server"`,
				)
				add(
					"agent",
					"host",
					"test-agent",
					`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-server")" = package; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-host"`,
				)
				add(
					"agent",
					"container",
					"second-agent",
					`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-host")" = package; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-container"`,
				)
			} else if scenario == "agent-first" {
				add(
					"agent",
					"host",
					"test-agent",
					`test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-host"`,
				)
				add(
					"agent",
					"container",
					"second-agent",
					`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-host")" = package; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-container"`,
				)
			} else {
				script := `cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-container"`
				selector := ""
				switch scenario {
				case "conflict":
					script = `printf "$LANG" > "$DURPDEPLOY_STAGE_DIR/from-container"`
				case "failure":
					script += "; exit 7"
				case "cancel":
					script += "; sleep 90"
				case "slow-package", "changed-package":
					selector = "test-agent"
				}
				add("agent", "host", selector, script)
			}
			add(
				"local",
				"",
				"",
				`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-container")" = package; test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; echo package-transfer-ok`,
			)
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
			if err := json.Unmarshal(
				s.api(t, "POST", base+"/deployments", map[string]int64{
					"release_id": release.ID, "environment_id": environmentID,
				}, 201),
				&deployment,
			); err != nil {
				t.Fatal(err)
			}
			want := "succeeded"
			switch scenario {
			case "conflict", "failure", "changed-package":
				want = "failed"
			case "cancel":
				s.cancelRunningStaging(t, deployment.ID)
				want = "cancelled"
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
			if want == "succeeded" &&
				!strings.Contains(logs, "package-transfer-ok") {
				t.Fatalf("missing final package read: %s", logs)
			}
			if strings.Contains(logs, "upstream-secret") {
				t.Fatal("package credential leaked")
			}
			s.assertTransferPurged(t, scenario, deployment.ID)
			s.assertRuntimeStagingPurged(t)
			files, err := os.ReadDir(workspace)
			if err != nil || len(files) != 1 || files[0].Name() != ".lock" {
				t.Fatalf("server package files retained: %v %v", files, err)
			}
		})
	}
}

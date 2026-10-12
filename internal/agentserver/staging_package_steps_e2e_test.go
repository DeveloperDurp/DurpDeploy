//go:build e2e && agentstaging

package agentserver_test

import "testing"

type packageStepSpec struct{ target, mode, selector, script string }

func (s *stagingE2E) addPackageScenarioSteps(
	t *testing.T,
	config packageSourceConfig,
) {
	t.Helper()
	scenario := config.scenario
	add := func(spec packageStepSpec) {
		s.addPackageStep(t, config, spec)
	}
	if scenario == "handoff" {
		add(packageStepSpec{
			"local",
			"",
			"",
			`test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-server"`,
		})
		add(packageStepSpec{
			"agent",
			"host",
			"test-agent",
			`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-server")" = package; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-host"`,
		})
		add(packageStepSpec{
			"agent",
			"container",
			"second-agent",
			`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-host")" = package; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-container"`,
		})
	} else if scenario == "agent-first" {
		add(packageStepSpec{
			"agent",
			"host",
			"test-agent",
			`test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-host"`,
		})
		add(packageStepSpec{
			"agent",
			"container",
			"second-agent",
			`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-host")" = package; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/from-container"`,
		})
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
		add(packageStepSpec{"agent", "host", selector, script})
	}
	add(packageStepSpec{
		"local",
		"",
		"",
		`test "$(cat "$DURPDEPLOY_STAGE_DIR/from-container")" = package; test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; echo package-transfer-ok`,
	})
}

func (s *stagingE2E) addPackageStep(
	t *testing.T,
	config packageSourceConfig,
	spec packageStepSpec,
) {
	t.Helper()
	script := "set -eu\ntest \"$(cat \"$ARTIFACT_PATH/package.txt\")\" = package\n" + spec.script
	step := map[string]any{
		"name": "package", "execution_target": spec.target,
		"interpreter": "bash", "timeout_seconds": 120,
	}
	if spec.target == "local" || spec.mode == "container" {
		step["container_image"] = "docker.io/library/bash:5.2"
		script += "\n! touch \"$ARTIFACT_PATH/write\"\nawk '$5 == \"/artifacts\" { if ($6 !~ /ro/ || $6 !~ /noexec/) exit 1; found=1 } END { exit !found }' /proc/self/mountinfo"
	}
	step["script_body"] = script
	if spec.target == "agent" {
		step["agent_execution_mode"] = spec.mode
	}
	if spec.selector != "" {
		step["agent_selectors"] = []string{spec.selector}
	}
	if config.scenario == "slow-package" && spec.target == "agent" {
		step["timeout_seconds"] = 2
	}
	s.api(t, "POST", config.base+"/steps", step, 201)
}

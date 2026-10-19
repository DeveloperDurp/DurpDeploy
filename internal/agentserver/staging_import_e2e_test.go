//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeploymentStagingImportFailureAPIE2E(t *testing.T) {
	runtime, err := exec.LookPath("podman")
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "podman")
	script := `#!/bin/sh
case "$*" in
  *'find /stage -mindepth 1'*) exit 7;;
esac
exec "$STAGING_TEST_RUNTIME" "$@"
`
	if err := os.WriteFile(wrapper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STAGING_TEST_RUNTIME", runtime)
	s := newStagingE2EWithRuntime(t, wrapper)
	decode := func(data []byte) int64 {
		t.Helper()
		var value struct{ ID int64 }
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		return value.ID
	}
	project := decode(
		s.api(
			t,
			"POST",
			"/projects",
			map[string]string{"name": "staging-import-failure"},
			201,
		),
	)
	environment := decode(
		s.api(
			t,
			"POST",
			"/environments",
			map[string]string{"name": "staging-import-failure"},
			201,
		),
	)
	s.api(
		t,
		"POST",
		"/admin/agents/test-agent/environments",
		map[string]int64{"environment_id": environment},
		204,
	)
	base := fmt.Sprintf("/projects/%d", project)
	s.api(t, "POST", base+"/steps", map[string]any{
		"name":                 "producer",
		"script_body":          `printf shared > "$DURPDEPLOY_STAGE_DIR/data"`,
		"execution_target":     "agent",
		"agent_execution_mode": "host",
		"agent_selectors":      []string{"test-agent"},
		"interpreter":          "bash",
	}, 201)
	s.api(t, "POST", base+"/steps", map[string]any{
		"name":             "consumer",
		"script_body":      `cat "$DURPDEPLOY_STAGE_DIR/data"`,
		"execution_target": "local",
		"container_image":  "harbor.durp.info/dockerhub/library/bash:5.2",
		"interpreter":      "bash",
	}, 201)
	release := decode(
		s.api(
			t,
			"POST",
			base+"/releases",
			map[string]string{"version": "1"},
			201,
		),
	)
	deployment := decode(
		s.api(
			t,
			"POST",
			base+"/deployments",
			map[string]int64{
				"release_id":     release,
				"environment_id": environment,
			},
			201,
		),
	)
	s.wait(t, deployment, "failed")
	logs := string(
		s.api(
			t,
			"GET",
			fmt.Sprintf("/deployments/%d/logs.txt", deployment),
			nil,
			200,
		),
	)
	if !strings.Contains(
		logs,
		`step "consumer": staging failed before execution: import staging snapshot`,
	) ||
		!strings.Contains(logs, "Step failed.") {
		t.Fatalf("missing import failure and failed step state: %s", logs)
	}
	s.assertRuntimeStagingPurged(t)
	s.api(t, "GET", "/deployments/not-an-id/staging", nil, 400)
	s.api(t, "GET", "/deployments/-1/staging", nil, 400)
}

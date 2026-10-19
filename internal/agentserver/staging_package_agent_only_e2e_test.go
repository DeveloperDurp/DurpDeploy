//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	agentartifact "github.com/DeveloperDurp/durpdeploy-agent/artifact"
)

func TestDeploymentStagingPackageAgentOnlyAPIE2E(t *testing.T) {
	s := newStagingE2EConfigured(t, stagingFixtureConfig{})
	base, environmentID := s.packageProject(t, "agent-only")
	s.packageSource(t, packageSourceConfig{base: base})
	s.api(t, "POST", base+"/variables", map[string]string{
		"name": "SELECTED", "value": "selected",
	}, 201)
	s.api(t, "POST", base+"/variables", map[string]string{
		"name": "UNRELATED", "value": "hidden",
	}, 201)
	steps := []map[string]any{
		{
			"name":                 "host",
			"interpreter":          "bash",
			"execution_target":     "agent",
			"agent_execution_mode": "host",
			"agent_selectors":      []string{"test-agent"},
			"variable_names":       []string{"SELECTED"},
			"script_body":          `set -eu; test "$SELECTED" = selected; test "${UNRELATED-unset}" = unset; test ! -e "$DURPDEPLOY_STAGE_DIR/package.txt"; cp "$ARTIFACT_PATH/package.txt" "$DURPDEPLOY_STAGE_DIR/output"`,
		},
		{
			"name":                 "container",
			"interpreter":          "bash",
			"execution_target":     "agent",
			"agent_execution_mode": "container",
			"agent_selectors":      []string{"second-agent"},
			"container_image":      "harbor.durp.info/dockerhub/library/bash:5.2",
			"variable_names":       []string{"SELECTED"},
			"script_body":          `set -eu; test "$SELECTED" = selected; test "${UNRELATED-unset}" = unset; test "$(cat "$DURPDEPLOY_STAGE_DIR/output")" = package; test "$(cat "$ARTIFACT_PATH/package.txt")" = package; ! touch "$ARTIFACT_PATH/write"; echo package-agent-only-ok`,
		},
	}
	for _, step := range steps {
		s.api(t, "POST", base+"/steps", step, 201)
	}
	var release, deployment struct{ ID int64 }
	decode := func(data []byte, target any) {
		t.Helper()
		if err := json.Unmarshal(data, target); err != nil {
			t.Fatal(err)
		}
	}
	decode(
		s.api(
			t,
			"POST",
			base+"/releases",
			map[string]string{"version": "v1"},
			201,
		),
		&release,
	)
	decode(s.api(t, "POST", base+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": environmentID,
	}, 201), &deployment)
	s.wait(t, deployment.ID, "succeeded")
	s.assertTransferPurged(t, "agent-only", deployment.ID)
	var saved struct {
		Runbook db.Runbook        `json:"runbook"`
		Version db.RunbookVersion `json:"version"`
	}
	body := map[string]any{
		"name":                "agent-package-runbook",
		"artifact_release_id": release.ID,
		"steps":               steps,
	}
	decode(s.api(t, "POST", base+"/runbooks", body, 201), &saved)
	delete(body, "artifact_release_id")
	body["keep_artifact_pin"] = true
	decode(
		s.api(
			t,
			"PUT",
			fmt.Sprintf("%s/runbooks/%d", base, saved.Runbook.ID),
			body,
			201,
		),
		&saved,
	)
	var execution db.RunbookExecution
	decode(
		s.api(
			t,
			"POST",
			fmt.Sprintf("%s/runbooks/%d/executions", base, saved.Runbook.ID),
			map[string]int64{
				"version_id": saved.Version.ID, "environment_id": environmentID,
			},
			201,
		),
		&execution,
	)
	executionPath := fmt.Sprintf("%s/runbook-executions/%d", base, execution.ID)
	deadline := time.Now().Add(time.Minute)
	for {
		var state struct{ Status string }
		decode(s.api(t, "GET", executionPath, nil, 200), &state)
		if state.Status == "succeeded" {
			break
		}
		if state.Status == "failed" || time.Now().After(deadline) {
			t.Fatalf(
				"runbook status=%s logs=%s",
				state.Status,
				s.api(t, "GET", executionPath+"/logs", nil, 200),
			)
		}
		time.Sleep(100 * time.Millisecond)
	}
	logs := string(
		s.api(
			t,
			"GET",
			executionPath+"/logs",
			nil,
			200,
		),
	)
	if !strings.Contains(logs, "package-agent-only-ok") {
		t.Fatalf("missing runbook execution: %s", logs)
	}
	rows, err := s.f.repo.Queries.ListStagingSnapshots(
		t.Context(),
		execution.DeploymentID,
	)
	if err != nil || len(rows) != 0 {
		t.Fatalf("runbook staging retained: %v %v", rows, err)
	}
	for _, directory := range s.directories {
		files, err := os.ReadDir(filepath.Join(directory, "staging"))
		if err != nil || len(files) != 0 {
			t.Fatalf("agent runbook files retained: %v %v", files, err)
		}
	}
	s.assertRuntimeStagingPurged(t)
}

func TestDeploymentStagingPackageLegacyAgentAPIE2E(t *testing.T) {
	s := newStagingE2EConfigured(t, stagingFixtureConfig{
		wrap: func(next http.Handler) http.Handler {
			return http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					r.Header.Del(agentartifact.CapabilityHeader)
					next.ServeHTTP(w, r)
				},
			)
		},
	})
	base, environmentID := s.packageProject(t, "legacy-agent")
	s.packageSource(t, packageSourceConfig{base: base})
	s.api(t, "POST", base+"/steps", map[string]string{
		"name":             "legacy",
		"execution_target": "agent",
		"script_body":      "echo should-not-run",
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
	if err := json.Unmarshal(
		s.api(t, "POST", base+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": environmentID,
		}, 201),
		&deployment,
	); err != nil {
		t.Fatal(err)
	}
	s.wait(t, deployment.ID, "failed")
	logs := string(
		s.api(
			t,
			"GET",
			fmt.Sprintf("/deployments/%d/logs.txt", deployment.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(logs, "artifact/1") ||
		strings.Contains(logs, "should-not-run") {
		t.Fatalf("missing package capability diagnostic: %s", logs)
	}
	s.assertTransferPurged(t, "legacy-agent", deployment.ID)
}

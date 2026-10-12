//go:build e2e && agentstaging

package agentserver_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentStagingAuditAPIE2E(t *testing.T) {
	// Given a deployment that fans out to two real agents.
	s := newStagingE2E(t)
	var project, environment, release, deployment struct{ ID int64 }
	decodeID := func(data []byte, target any) {
		t.Helper()
		if err := json.Unmarshal(data, target); err != nil {
			t.Fatal(err)
		}
	}
	decodeID(s.api(t, "POST", "/projects",
		map[string]string{"name": "staging-audit"}, 201), &project)
	decodeID(s.api(t, "POST", "/environments",
		map[string]string{"name": "staging-audit"}, 201), &environment)
	for _, agentID := range []string{"test-agent", "second-agent"} {
		s.api(t, "POST", "/admin/agents/"+agentID+"/environments",
			map[string]int64{"environment_id": environment.ID}, 204)
	}
	base := fmt.Sprintf("/projects/%d", project.ID)
	s.api(t, "POST", base+"/steps", map[string]any{
		"name": "publish", "execution_target": "agent",
		"agent_execution_mode": "host", "interpreter": "bash",
		"script_body":     `printf artifact > "$DURPDEPLOY_STAGE_DIR/$LANG"`,
		"timeout_seconds": 120,
	}, 201)
	decodeID(s.api(t, "POST", base+"/releases",
		map[string]string{"version": "v1"}, 201), &release)

	// When the deployment succeeds and its managed staging is purged.
	decodeID(s.api(t, "POST", base+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": environment.ID,
	}, 201), &deployment)
	s.wait(t, deployment.ID, "succeeded")
	s.assertRuntimeStagingPurged(t)
	s.assertTransferPurged(t, "audit", deployment.ID)

	// Then the public audit API still identifies both authenticated agents.
	var history struct {
		Items []db.AuditLog `json:"items"`
	}
	if err := json.Unmarshal(s.api(
		t,
		"GET",
		"/admin/audit?action=publish_staging",
		nil,
		200,
	), &history); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"test-agent": true, "second-agent": true}
	for _, entry := range history.Items {
		if !entry.EntityID.Valid || entry.EntityID.Int64 != deployment.ID ||
			entry.UserID.Valid || !entry.Details.Valid {
			t.Fatalf("unexpected audit entry: %+v", entry)
		}
		var details struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(
			[]byte(entry.Details.String),
			&details,
		); err != nil {
			t.Fatal(err)
		}
		if details.AgentID != "test-agent" &&
			details.AgentID != "second-agent" {
			t.Fatalf("unexpected audit agent: %q", details.AgentID)
		}
		delete(want, details.AgentID)
	}
	if len(want) != 0 {
		t.Fatalf("missing agent audit entries: %v", want)
	}
}

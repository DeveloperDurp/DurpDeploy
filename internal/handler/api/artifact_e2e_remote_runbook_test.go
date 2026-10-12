//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
)

func (f *artifactE2E) verifyRemoteRunbookPackage(
	t *testing.T,
	releaseID int64,
) {
	t.Helper()
	step := map[string]any{
		"name": "remote", "script_body": "true", "interpreter": "bash",
		"execution_target": "agent", "agent_execution_mode": "host",
	}
	body := map[string]any{
		"name": "remote-maintenance", "artifact_release_id": releaseID,
		"steps": []map[string]any{step},
	}
	data := f.api(t, "POST", f.base()+"/runbooks", body, 201)
	var saved struct {
		Runbook db.Runbook        `json:"runbook"`
		Version db.RunbookVersion `json:"version"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	delete(body, "artifact_release_id")
	body["keep_artifact_pin"] = true
	f.api(
		t,
		"PUT",
		fmt.Sprintf("%s/runbooks/%d", f.base(), saved.Runbook.ID),
		body,
		201,
	)
	f.web(
		t,
		"POST",
		fmt.Sprintf("/projects/%d/runbooks", f.project.ID),
		url.Values{
			"name":                {"web-remote-maintenance"},
			"artifact_release_id": {fmt.Sprint(releaseID)},
			"step_name":           {"remote"}, "step_script": {"true"},
			"step_interpreter": {"bash"}, "step_target": {"agent"},
			"step_agent_mode": {"host"}, "step_timeout": {"0"},
			"step_retries": {"0"},
			"step_image":   {""}, "step_selectors": {""},
			"step_variable_names": {""},
		},
		303,
	)
}

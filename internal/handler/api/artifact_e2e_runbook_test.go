//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func (f *artifactE2E) verifyRunbookPackage(
	t *testing.T,
	releaseID int64,
	script string,
) {
	t.Helper()
	base := f.base()
	webBase := fmt.Sprintf("/projects/%d", f.project.ID)
	data := f.api(
		t,
		"POST",
		base+"/runbooks",
		map[string]any{
			"name":                "maintenance",
			"artifact_release_id": releaseID,
			"steps": []map[string]any{
				{
					"name":            "read",
					"script_body":     script,
					"interpreter":     "bash",
					"container_image": "docker.io/library/bash:5.2",
				},
			},
		},
		201,
	)
	var saved struct {
		Runbook db.Runbook        `json:"runbook"`
		Version db.RunbookVersion `json:"version"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"GET",
		fmt.Sprintf(
			"%s/runbooks/%d/versions/%d",
			base,
			saved.Runbook.ID,
			saved.Version.ID,
		),
		nil,
		200,
	)
	data = f.api(
		t,
		"POST",
		fmt.Sprintf("%s/runbooks/%d/executions", base, saved.Runbook.ID),
		map[string]int64{
			"environment_id": f.environment.ID,
			"version_id":     saved.Version.ID,
		},
		201,
	)
	var execution db.RunbookExecution
	if err := json.Unmarshal(data, &execution); err != nil {
		t.Fatal(err)
	}
	f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
	form := f.web(t, "GET", webBase+"/runbooks/new", nil, 200)
	if !strings.Contains(form, `name="artifact_release_id"`) {
		t.Fatal("runbook package selection missing")
	}
	f.web(
		t,
		"POST",
		webBase+"/runbooks",
		url.Values{
			"name":                {"web-maintenance"},
			"artifact_release_id": {fmt.Sprint(releaseID)},
			"step_name":           {"read"},
			"step_script":         {script},
			"step_interpreter":    {"bash"},
			"step_target":         {"local"},
			"step_image":          {"docker.io/library/bash:5.2"},
			"step_timeout":        {"0"},
			"step_retries":        {"0"},
			"step_selectors":      {""},
			"step_variable_names": {""},
		},
		303,
	)
}

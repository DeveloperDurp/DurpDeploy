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

func (f *artifactE2E) verifyOrphanedPinEdits(
	t *testing.T,
	version db.RunbookVersion,
	script string,
) {
	t.Helper()
	path := fmt.Sprintf("%s/runbooks/%d", f.base(), version.RunbookID)
	body := map[string]any{
		"keep_artifact_pin": true,
		"steps": []map[string]any{
			{
				"name":            "read",
				"script_body":     script,
				"interpreter":     "bash",
				"container_image": "docker.io/library/bash:5.2",
			},
		},
	}
	data := f.api(t, "PUT", path, body, 201)
	var saved struct {
		Version db.RunbookVersion `json:"version"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		t.Fatal(err)
	}
	pin := f.api(
		t,
		"GET",
		fmt.Sprintf("%s/versions/%d", path, saved.Version.ID),
		nil,
		200,
	)
	if !strings.Contains(string(pin), `"sha256"`) {
		t.Fatal("API edit dropped the pin")
	}
	webPath := fmt.Sprintf(
		"/projects/%d/runbooks/%d",
		f.project.ID,
		version.RunbookID,
	)
	form := f.web(t, "GET", webPath+"/edit", nil, 200)
	if !strings.Contains(form, `value="-1" selected`) {
		t.Fatal("web edit does not default to keeping the orphaned pin")
	}
	f.web(
		t,
		"POST",
		webPath+"/save",
		url.Values{
			"artifact_release_id": {"-1"},
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
	data = f.api(t, "GET", path, nil, 200)
	var detail struct {
		Versions []db.RunbookVersion `json:"versions"`
	}
	if err := json.Unmarshal(
		data,
		&detail,
	); err != nil ||
		len(detail.Versions) == 0 {
		t.Fatalf("missing edited versions: %v", err)
	}
	latest := detail.Versions[0]
	data = f.api(
		t,
		"POST",
		path+"/executions",
		map[string]int64{
			"environment_id": f.environment.ID,
			"version_id":     latest.ID,
		},
		201,
	)
	var execution db.RunbookExecution
	if err := json.Unmarshal(data, &execution); err != nil {
		t.Fatal(err)
	}
	f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
}

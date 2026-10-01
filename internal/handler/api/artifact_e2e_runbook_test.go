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
	"golang.org/x/net/html"
)

func (f *artifactE2E) verifyRunbookPackage(
	t *testing.T,
	releaseID int64,
	script string,
) db.RunbookVersion {
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
	f.verifyRunbookArtifactValidation(t, saved.Runbook.ID, releaseID)
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
	assertArtifactSelectInSaveForm(t, form)
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
	return saved.Version
}

func assertArtifactSelectInSaveForm(t *testing.T, markup string) {
	t.Helper()
	root, err := html.Parse(strings.NewReader(markup))
	if err != nil {
		t.Fatal(err)
	}
	selector := findArtifactSelect(root)
	if selector == nil {
		t.Fatal("artifact release selector is missing")
	}
	for parent := selector.Parent; parent != nil; parent = parent.Parent {
		if parent.Type == html.ElementNode && parent.Data == "form" {
			return
		}
	}
	t.Fatal("artifact release selector is outside the save form")
}

func findArtifactSelect(root *html.Node) *html.Node {
	for node := range root.Descendants() {
		if node.Type != html.ElementNode || node.Data != "select" {
			continue
		}
		for _, attribute := range node.Attr {
			if attribute.Key == "name" &&
				attribute.Val == "artifact_release_id" {
				return node
			}
		}
	}
	return nil
}

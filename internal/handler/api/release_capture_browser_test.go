//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestReleaseCaptureBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	step, err := f.h.repo.CreateStepWithPlacement(
		t.Context(),
		db.CreateStepParams{
			ProjectID:          f.project.ID,
			Name:               "Capture",
			ScriptBody:         "echo old",
			Interpreter:        "bash",
			AgentExecutionMode: "host",
		},
		"agent",
		[]string{"old-agent"},
	)
	if err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	list := fmt.Sprintf("/projects/%d/releases", f.project.ID)
	b.navigateBackTest(t, f.baseURL+list)
	b.wait(t, `document.querySelector('#release-version') && window.htmx`)
	b.evaluate(
		t,
		`document.querySelector('#release-version').value='capture-v1';document.querySelector('#release-version').form.requestSubmit();true`,
	)
	b.wait(
		t,
		`document.body.textContent.includes('capture-v1') && !document.querySelector('.htmx-request')`,
	)
	releases, err := f.h.repo.Queries.ListReleasesByProject(
		t.Context(),
		f.project.ID,
	)
	if err != nil || len(releases) != 1 {
		t.Fatalf("browser release creation: %+v: %v", releases, err)
	}
	releaseID := releases[0].ID
	// A public step edit changes script and placement together.
	f.api(
		t,
		"PUT",
		fmt.Sprintf("/api/v1/projects/%d/steps/%d", f.project.ID, step.ID),
		map[string]any{
			"name": step.Name, "script_body": "echo new", "execution_target": "local", "container_image": "harbor.durp.info/dockerhub/library/bash:5.2", "interpreter": "bash",
		},
		200,
	)
	path := fmt.Sprintf("%s/%d", list, releaseID)
	assertRelease := func(script, target, selector string) {
		t.Helper()
		var release struct {
			StepsJSON string `json:"steps_json"`
		}
		decodeLifecycleTest(
			t,
			f.api(t, "GET", "/api/v1"+path, nil, 200),
			&release,
		)
		if !strings.Contains(release.StepsJSON, script) ||
			!strings.Contains(
				release.StepsJSON,
				`"execution_target":"`+target+`"`,
			) ||
			(selector != "" && !strings.Contains(release.StepsJSON, selector)) ||
			(selector == "" && strings.Contains(release.StepsJSON, "old-agent")) {
			t.Fatalf("wrong release source: %s", release.StepsJSON)
		}
	}
	assertRelease("echo old", "agent", "old-agent")
	b.navigateBackTest(t, f.baseURL+path)
	b.evaluate(
		t,
		`window.confirm=()=>true;window.captureRefresh=document.querySelector('form[action$="/refresh"]');window.captureRefresh.requestSubmit();true`,
	)
	b.wait(
		t,
		`document.querySelector('form[action$="/refresh"]') && document.querySelector('form[action$="/refresh"]')!==window.captureRefresh && !document.querySelector('.htmx-request,.htmx-settling')`,
	)
	assertRelease("echo new", "local", "")
	b.captureNavigation(t, "release-consistent-capture")
}

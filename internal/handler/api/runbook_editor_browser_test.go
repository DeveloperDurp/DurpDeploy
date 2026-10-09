//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestRunbookEditorBrowserE2E(t *testing.T) {
	// Given an immutable runbook with local and agent steps.
	f := newArtifactE2E(t)
	if _, err := f.h.repo.Queries.CreateAgent(t.Context(),
		db.CreateAgentParams{ID: "editor-agent", Name: "Editor agent"},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.AddAgentLabel(t.Context(),
		db.AddAgentLabelParams{AgentID: "editor-agent", Label: "canary"},
	); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/v1/projects/%d/runbooks", f.project.ID)
	created := f.api(t, "POST", base, json.RawMessage(`{
		"name":"editor check","steps":[
			{"name":"remote","script_body":"printf remote",
			 "execution_target":"agent","agent_selectors":["canary"],
			 "variable_names":["DB_HOST","API_TOKEN"]},
			{"name":"local & <one>","script_body":"printf local",
			 "container_image":"alpine:3.20","network_mode":"bridge"}
		]}`), 201)
	var saved struct {
		Runbook db.Runbook `json:"runbook"`
	}
	if err := json.Unmarshal(created, &saved); err != nil {
		t.Fatal(err)
	}
	book := fmt.Sprintf("/projects/%d/runbooks/%d",
		f.project.ID, saved.Runbook.ID)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, f.baseURL+book)
	b.wait(
		t,
		`document.querySelector('.page-header [x-data="backNavigation"]')?.nextElementSibling === null`,
	)
	// Reach the editor through a real boosted link.
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('a[href=%q]').click(); true`, book+"/edit",
	))
	b.wait(
		t,
		`document.querySelectorAll('#runbook-version-form [name=step_name]').length === 2`,
	)
	b.evaluate(
		t,
		`document.querySelector('#runbook-version-form button.btn-secondary').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('dialog').matches(':modal') && document.querySelector('dialog [name=step_selectors]').value === 'canary'`,
	)
	b.evaluate(
		t,
		`document.querySelector('dialog [name=step_name]').value = 'discarded'; document.querySelector('dialog [name=step_name]').dispatchEvent(new Event('input')); document.querySelector('dialog button.btn-ghost').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('dialog').open && document.querySelector('#runbook-version-form [name=step_name]').value === 'remote'`,
	)
	b.evaluate(
		t,
		`[...document.querySelectorAll('button')].find(e => e.textContent === 'Add step').click(); true`,
	)
	b.wait(t, `document.querySelector('dialog').matches(':modal')`)
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		b.call(
			t,
			"Input.dispatchMouseEvent",
			map[string]any{
				"type":       kind,
				"x":          1,
				"y":          100,
				"button":     "left",
				"clickCount": 1,
			},
			&struct{}{},
		)
	}
	b.wait(
		t,
		`!document.querySelector('dialog').open && document.querySelectorAll('#runbook-version-form [name=step_name]').length === 2`,
	)
	b.evaluate(
		t,
		`[...document.querySelectorAll('button')].find(e => e.textContent === 'Add step').click(); true`,
	)
	b.wait(t, `document.querySelector('dialog').matches(':modal')`)
	b.evaluate(t, `(() => {
 const values = {step_name:'temporary', step_script:'echo temporary', step_image:'alpine:3.20'};
 for (const [name, value] of Object.entries(values)) {
  const input = document.querySelector('dialog [name=' + name + ']:not([type=hidden])');
  input.value = value; input.dispatchEvent(new Event('input'));
 }
 document.querySelector('dialog button[type=submit]').click(); return true;
})()`)
	b.wait(
		t,
		`!document.querySelector('dialog').open && document.querySelectorAll('#runbook-version-form [name=step_name]').length === 3`,
	)
	b.evaluate(
		t,
		`document.querySelectorAll('#runbook-version-form button.btn-secondary')[2].click(); true`,
	)
	b.wait(t, `document.querySelector('dialog').matches(':modal')`)
	b.evaluate(
		t,
		`window.confirm = () => false; document.querySelector('dialog button.btn-error').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('dialog').open && document.querySelectorAll('#runbook-version-form [name=step_name]').length === 3`,
	)
	b.evaluate(
		t,
		`window.confirm = () => true; document.querySelector('dialog button.btn-error').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('dialog').open && document.querySelectorAll('#runbook-version-form [name=step_name]').length === 2`,
	)
	b.evaluate(
		t,
		`document.querySelector('[aria-label="Move step down"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#runbook-version-form [name=step_name]').value === 'local & <one>'`,
	)
	b.evaluate(
		t,
		`document.querySelector('#runbook-version-form button.btn-secondary').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('dialog').matches(':modal') && document.querySelector('dialog [name=step_image]:not([type=hidden])')?.value === 'alpine:3.20' && document.querySelector('dialog [name=step_network]')?.value === 'bridge'`,
	)
	b.captureNavigation(t, "runbook-step")
	b.evaluate(
		t,
		`document.querySelector('dialog button[type=submit]').click(); true`,
	)
	b.wait(t, `!document.querySelector('dialog').open`)
	b.evaluate(
		t,
		`document.querySelector('#runbook-version-form').requestSubmit(); true`,
	)
	b.waitBackTestURL(t, f.baseURL+book)
	// Then the public API exposes the new version with all fields intact.
	var detail struct {
		Versions []db.RunbookVersion `json:"versions"`
	}
	if err := json.Unmarshal(f.api(t, "GET",
		fmt.Sprintf("%s/%d", base, saved.Runbook.ID), nil, 200),
		&detail); err != nil {
		t.Fatal(err)
	}
	if len(detail.Versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(detail.Versions))
	}
	var version struct {
		Steps []struct {
			Name           string   `json:"name"`
			ContainerImage string   `json:"container_image"`
			NetworkMode    string   `json:"network_mode"`
			AgentSelectors []string `json:"agent_selectors"`
			VariableNames  []string `json:"variable_names"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(f.api(t, "GET", fmt.Sprintf(
		"%s/%d/versions/%d", base, saved.Runbook.ID, detail.Versions[0].ID,
	), nil, 200), &version); err != nil {
		t.Fatal(err)
	}
	if len(version.Steps) != 2 ||
		version.Steps[0].Name != "local & <one>" ||
		version.Steps[0].ContainerImage != "alpine:3.20" ||
		version.Steps[0].NetworkMode != "bridge" ||
		version.Steps[1].Name != "remote" ||
		fmt.Sprint(version.Steps[1].AgentSelectors) != "[canary]" ||
		fmt.Sprint(version.Steps[1].VariableNames) != "[DB_HOST API_TOKEN]" {
		t.Fatalf("saved steps = %+v", version.Steps)
	}
	b.navigateBackTest(t, fmt.Sprintf(
		"%s/projects/%d/runbooks/new", f.baseURL, f.project.ID,
	))
	b.wait(
		t,
		`document.querySelectorAll('#runbook-version-form [name=step_name]').length === 1 && document.querySelector('[aria-label="Move step up"]').disabled && document.querySelector('[aria-label="Move step down"]').disabled`,
	)
	b.captureNavigation(t, "runbook-new")
}

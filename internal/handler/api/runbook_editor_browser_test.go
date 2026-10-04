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
			 "container_image":"alpine:3.20"}
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
	// Reach the editor through a real boosted link.
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('a[href=%q]').click(); true`, book+"/edit",
	))
	b.wait(
		t,
		`document.querySelectorAll('[name=step_name]').length === 2 && document.querySelector('[name=step_selectors]:not([type=hidden])').value === 'canary' && document.querySelector('[name=step_variable_names]').value === 'DB_HOST, API_TOKEN'`,
	)
	b.captureNavigation(t, "runbook-editor")
	// When adding, removing, and moving whole steps in both directions.
	b.evaluate(
		t,
		`document.querySelector('button.btn-secondary').click(); true`,
	)
	b.wait(t, `document.querySelectorAll('[name=step_name]').length === 3`)
	b.evaluate(
		t,
		`[...document.querySelectorAll('button')].filter(e => e.textContent === 'Remove')[2].click(); document.querySelector('[aria-label="Move step down"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelectorAll('[name=step_name]').length === 2 && document.querySelector('[name=step_name]').value === 'local & <one>' && document.querySelectorAll('[name=step_selectors]:not([type=hidden])')[1].value === 'canary'`,
	)
	b.evaluate(
		t,
		`document.querySelectorAll('[aria-label="Move step up"]')[1].click(); true`,
	)
	b.wait(t, `document.querySelector('[name=step_name]').value === 'remote'`)
	b.evaluate(
		t,
		`document.querySelector('[aria-label="Move step down"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[name=step_name]').value === 'local & <one>'`,
	)
	b.captureNavigation(t, "runbook-reordered")
	if string(
		b.evaluate(
			t,
			`[...document.querySelectorAll('[name=step_name]')].every(input => { const card = input.closest('.card'); const target = card.querySelector('[name=step_target]').value; return ['step_image', 'step_selectors'].every(name => { const label = card.querySelector('label > [name=' + name + ']').parentElement; return (label.getBoundingClientRect().height > 0) === (target === (name === 'step_image' ? 'local' : 'agent')); }); })`,
		),
	) != "true" {
		t.Fatal("reordered step shows an inactive execution-target field")
	}
	b.evaluate(
		t,
		`document.querySelector('form[x-data="runbookEditor"]').requestSubmit(); true`,
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
		`document.querySelectorAll('[name=step_name]').length === 1 && [...document.querySelectorAll('button')].find(e => e.textContent === 'Remove').disabled && document.querySelector('[aria-label="Move step up"]').disabled && document.querySelector('[aria-label="Move step down"]').disabled`,
	)
	b.captureNavigation(t, "runbook-new")
}

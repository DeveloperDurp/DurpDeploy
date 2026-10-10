//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestLifecycleVariablesBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var lc struct {
		ID int64 `json:"id"`
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "Shared pipeline"},
			201,
		),
		&lc,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/lifecycles/%d/stages", lc.ID),
		map[string]int64{"environment_id": f.environment.ID},
		201,
	)
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lc.ID},
		200,
	)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, fmt.Sprintf("%s/lifecycles/%d", f.baseURL, lc.ID))
	b.captureNavigation(t, "lifecycle-empty")
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/lifecycles/%d/variables", lc.ID),
		map[string]any{
			"name":   "TOKEN",
			"value":  "browser-secret-sentinel",
			"secret": true,
		},
		201,
	)
	b.evaluate(
		t,
		`document.querySelector('#shared-variables details').open = true; true`,
	)
	b.captureNavigation(t, "lifecycle-add")
	b.evaluate(
		t,
		`document.getElementById('shared-name').value='REGION'; document.getElementById('shared-value').value='shared-default'; document.querySelector('[data-shared-variable-form]').requestSubmit(); true`,
	)
	b.wait(
		t,
		`!!document.querySelector('[data-shared-variable="REGION"]') && !document.querySelector('.htmx-request')`,
	)
	b.captureNavigation(t, "lifecycle-populated")
	b.evaluate(
		t,
		`[...document.querySelectorAll('[data-shared-variable="TOKEN"] a')].find(e=>e.getClientRects().length).click(); true`,
	)
	b.wait(t, `document.querySelector('#shared-name')?.value === 'TOKEN'`)
	if string(
		b.evaluate(
			t,
			`document.getElementById('shared-value').value === '' && document.getElementById('shared-value').type === 'password' && !document.body.innerHTML.includes('browser-secret-sentinel')`,
		),
	) != "true" {
		t.Fatal("secret edit exposes plaintext")
	}
	b.captureNavigation(t, "lifecycle-secret-edit")
	b.evaluate(
		t,
		`document.querySelector('[data-shared-variable-form]').requestSubmit(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('[data-shared-variable-form]').hasAttribute('hx-put')`,
	)
	b.evaluate(
		t,
		`document.querySelector('#shared-variables details').open=true; document.getElementById('shared-name').value='REGION'; document.getElementById('shared-value').value='keep-on-error'; document.querySelector('[data-shared-variable-form]').requestSubmit(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#shared-variables [role=alert]')?.textContent.includes('already exists')`,
	)
	b.captureNavigation(t, "lifecycle-validation")
	if string(
		b.evaluate(
			t,
			`document.getElementById('shared-value').value === 'keep-on-error'`,
		),
	) != "true" {
		t.Fatal("validation lost the submitted nonsecret value")
	}
	b.evaluate(
		t,
		`document.querySelector('[data-shared-variable="REGION"] a').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-shared-variable-form]')?.hasAttribute('hx-put')`,
	)
	b.captureNavigation(t, "lifecycle-edit")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/lifecycles/%d/variables", f.baseURL, lc.ID),
	)
	b.captureNavigation(t, "lifecycle-standalone")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/variables", f.baseURL, f.project.ID),
	)
	b.captureNavigation(t, "project-inherited")
	b.evaluate(
		t,
		`[...document.querySelectorAll('[data-override-for="TOKEN"]')].find(e=>e.getClientRects().length).click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#variables-content input[name=name]').value === 'TOKEN' && document.querySelector('#variables-content input[name=secret]').checked && document.querySelector('#variables-content input[name=value]').type === 'password' && document.querySelector('#variables-content input[name=value]').value === ''`,
	)
	b.evaluate(
		t,
		`[...document.querySelectorAll('[data-override-for="REGION"]')].find(e=>e.getClientRects().length).click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#variables-content input[name=name]').value === 'REGION' && document.activeElement.name === 'value'`,
	)
	b.evaluate(
		t,
		`const form=document.querySelector('#variables-content form'); form.querySelector('[name=value]').value='project-value'; form.requestSubmit(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-inherited-variables]').textContent.includes('Project override')`,
	)
	b.captureNavigation(t, "project-overridden")
	b.evaluate(
		t,
		`window.confirm=()=>true; [...document.querySelectorAll('[data-inherited-variables] button')].find(e=>e.getClientRects().length && e.textContent==='Reset to inherited').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('[data-inherited-variables]').textContent.includes('Project override') && document.querySelector('[data-inherited-variables]').textContent.includes('shared-default')`,
	)
	b.captureNavigation(t, "project-reset")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d", f.baseURL, f.project.ID),
	)
	b.captureNavigation(t, "project-overview")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/edit", f.baseURL, f.project.ID),
	)
	b.captureNavigation(t, "project-assignment-admin")
	// Viewer inherits the values but cannot see either write affordance.
	user, err := f.h.repo.Queries.GetUserByEmail(
		t.Context(),
		"artifact-e2e@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID,
			UserID:    user.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.UpdateUser(t.Context(),
		db.UpdateUserParams{ID: user.ID, Name: user.Name, Role: "deployer"},
	); err != nil {
		t.Fatal(err)
	}
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/edit", f.baseURL, f.project.ID),
	)
	if string(
		b.evaluate(
			t,
			`[...document.querySelectorAll('select[name=lifecycle_id] option')].filter(o=>o.value !== '').length === 1`,
		),
	) != "true" {
		t.Fatal("nonadmin can assign a lifecycle")
	}
	b.captureNavigation(t, "project-assignment-readonly")
	b.evaluate(
		t,
		`document.querySelector('select[name=lifecycle_id]').value=''; document.querySelector('[data-project-form] form').requestSubmit(); true`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname === '/projects/%d' && !document.querySelector('[data-inherited-variables]')`,
			f.project.ID,
		),
	)
	b.captureNavigation(t, "project-assignment-removed")
	b.navigateBackTest(t, f.baseURL+"/projects/new")
	b.captureNavigation(t, "project-assignment-new-readonly")
	b.navigateBackTest(t, fmt.Sprintf("%s/lifecycles/%d", f.baseURL, lc.ID))
	if string(
		b.evaluate(
			t,
			`!document.querySelector('input[name=_method][value=delete]') && !document.querySelector('[data-lifecycle-stage-action]') && !document.querySelector('[data-lifecycle-environment-assignment]')`,
		),
	) != "true" {
		t.Fatal("deployer has lifecycle delete or stage write controls")
	}
	b.captureNavigation(t, "lifecycle-deployer")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/environments/%d/edit", f.baseURL, f.environment.ID),
	)
	if string(
		b.evaluate(t, `!document.querySelector('button[hx-delete]')`),
	) != "true" {
		t.Fatal("deployer has environment delete control")
	}
	b.captureNavigation(t, "environment-deployer")
	if err := f.h.repo.Queries.UpdateUser(
		t.Context(),
		db.UpdateUserParams{ID: user.ID, Name: user.Name, Role: "admin"},
	); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lc.ID},
		200,
	)
	if err := f.h.repo.Queries.UpdateUser(
		t.Context(),
		db.UpdateUserParams{ID: user.ID, Name: user.Name, Role: "viewer"},
	); err != nil {
		t.Fatal(err)
	}
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/variables", f.baseURL, f.project.ID),
	)
	b.wait(t, `!!document.querySelector('[data-inherited-variables]')`)
	if string(
		b.evaluate(
			t,
			`document.querySelectorAll('[data-inherited-variables] button').length === 0 && !document.querySelector('[data-inherited-variables] a')`,
		),
	) != "true" {
		t.Fatal("viewer has lifecycle writes")
	}
	b.captureNavigation(t, "project-viewer")
}

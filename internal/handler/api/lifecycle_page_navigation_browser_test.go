//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestLifecyclePageNavigationBrowserE2E(t *testing.T) {
	// Given: a lifecycle with shared variables and another name for validation.
	f := newArtifactE2E(t)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Full page pipeline"}, 201), &lifecycle)
	f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Duplicate pipeline"}, 201)
	path := fmt.Sprintf("/lifecycles/%d", lifecycle.ID)
	api := "/api/v1" + path
	f.api(t, "POST", api+"/variables", map[string]string{
		"name": "REGION", "value": "page-shared-value",
	}, 201)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 375, "height": 900, "deviceScaleFactor": 1,
		"mobile": false,
	}, &struct{}{})
	b.navigateBackTest(t, f.baseURL+"/lifecycles")

	// When: opening the lifecycle card, including the normal mobile flow.
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('[data-resource-card] a[href=%q]').click(); true`,
		path,
	))
	b.wait(t, fmt.Sprintf(
		`location.pathname === %q || document.querySelector('#project-edit-dialog')?.open`,
		path,
	))
	// Then: the full page contains stages, settings and the shared editor.
	if string(b.evaluate(t, fmt.Sprintf(
		`location.pathname === %q && !document.querySelector('dialog[open]') && !!document.querySelector('#shared-variables [data-shared-variable="REGION"]') && !!document.querySelector('#lifecycle-settings-form')`,
		path,
	))) != "true" {
		t.Fatal("lifecycle card opened a modal or omitted shared variables")
	}
	b.captureNavigation(t, "lifecycle-card-full-page", func() {
		b.assertVariableTableVisibility(t)
	})

	// Stage mutations keep the full workspace and its shared editor mounted.
	b.evaluate(t, fmt.Sprintf(
		`(() => { const form=document.querySelector('[data-lifecycle-environment-assignment]'); form.querySelector('select').value=%q; form.requestSubmit(); return true; })()`,
		fmt.Sprint(f.environment.ID),
	))
	b.wait(
		t,
		`!!document.querySelector('[data-lifecycle-stage-action="delete"]') && !!document.querySelector('#shared-variables') && !document.querySelector('.htmx-request')`,
	)
	var saved struct {
		Lifecycle struct{ Name string }
		Stages    []struct{ ID int64 }
	}
	decodeLifecycleTest(t, f.api(t, "GET", api, nil, 200), &saved)
	if len(saved.Stages) != 1 {
		t.Fatal("full-page stage addition did not persist")
	}
	b.evaluate(
		t,
		`window.confirm=()=>true; [...document.querySelectorAll('[data-lifecycle-stage-action="delete"]')].find(e=>e.getClientRects().length).click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('[data-lifecycle-stage-action="delete"]') && !!document.querySelector('#shared-variables') && !document.querySelector('.htmx-request')`,
	)
	decodeLifecycleTest(t, f.api(t, "GET", api, nil, 200), &saved)
	if len(saved.Stages) != 0 {
		t.Fatal("full-page stage removal did not persist")
	}

	// Settings validation retains the shared editor and submitted description.
	b.evaluate(
		t,
		`(() => { const form=document.getElementById('lifecycle-settings-form'); form.querySelector('[name=name]').value='Duplicate pipeline'; form.querySelector('textarea').value='retained description'; form.requestSubmit(); return true; })()`,
	)
	b.wait(
		t,
		`!!document.querySelector('#lifecycle-settings-form .text-error') && document.querySelector('#lifecycle-description')?.value==='retained description' && !!document.querySelector('#shared-variables')`,
	)
	b.captureNavigation(t, "lifecycle-page-settings-validation")
	b.evaluate(
		t,
		`document.querySelector('#lifecycle-name').value='Saved pipeline'; document.querySelector('button[form="lifecycle-settings-form"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname==='/lifecycles' && document.querySelector('main').textContent.includes('Saved pipeline')`,
	)
	decodeLifecycleTest(t, f.api(t, "GET", api, nil, 200), &saved)
	if saved.Lifecycle.Name != "Saved pipeline" {
		t.Fatal("full-page header Save did not persist")
	}

	// Back discards unsaved settings; deletion still confirms and reaches the API.
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('[data-resource-card] a[href=%q]').click(); true`,
		path,
	))
	b.wait(t, `!!document.querySelector('#lifecycle-name')`)
	b.evaluate(
		t,
		`document.querySelector('#lifecycle-name').value='Discarded'; document.querySelector('.page-header a[x-data="backNavigation"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname==='/lifecycles' && !!document.querySelector('[data-resource-card]')`,
	)
	decodeLifecycleTest(t, f.api(t, "GET", api, nil, 200), &saved)
	if saved.Lifecycle.Name != "Saved pipeline" {
		t.Fatal("Back persisted unsaved settings")
	}
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('[data-resource-card] a[href=%q]').click(); true`,
		path,
	))
	b.wait(
		t,
		`!!document.querySelector('#lifecycle-settings-form') && !document.querySelector('.htmx-request, .htmx-settling')`,
	)
	b.evaluate(
		t,
		`window.confirm=()=>false; document.querySelector('form[hx-confirm^="Delete this lifecycle"] button').click(); true`,
	)
	f.api(t, "GET", api, nil, 200)
	b.evaluate(
		t,
		`window.confirm=()=>true; document.querySelector('form[hx-confirm^="Delete this lifecycle"] button').click(); true`,
	)
	b.wait(
		t,
		`location.pathname==='/lifecycles' && !document.querySelector('#lifecycle-settings-form')`,
	)
	f.api(t, "GET", api, nil, 404)
}

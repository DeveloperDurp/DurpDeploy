//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestStepAddModalBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	api := fmt.Sprintf("/api/v1/projects/%d/steps", f.project.ID)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("/projects/%d/steps-page", f.project.ID)
	b.navigateBackTest(t, f.baseURL+path)
	open := func() {
		b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
		b.evaluate(
			t,
			`document.querySelector('[x-ref="addStepButton"]').click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('#step-edit-dialog')?.matches(':modal') && document.querySelector('#step-edit-content form[data-step-add-form]')`,
		)
	}
	open()
	b.captureNavigation(t, "step-add-modal", func() {
		b.wait(
			t,
			`document.getAnimations().every(a => a.playState !== 'running')`,
		)
		if string(b.evaluate(t, `(() => {
 const dialog = document.querySelector('#step-edit-dialog');
 const box = dialog.querySelector('.modal-box');
 box.scrollTop = box.scrollHeight;
 const save = dialog.querySelector('.page-header button[type="submit"]').getBoundingClientRect();
 return box.scrollWidth <= box.clientWidth && save.top >= box.getBoundingClientRect().top &&
 save.bottom <= innerHeight && (innerWidth >= 768 || save.height >= 44) &&
 !dialog.querySelector('[hx-delete]') && !document.querySelector('#step-list form');
})()`)) != "true" {
			t.Fatal("Add Step modal clips fields or hides Save")
		}
	})
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content input[name="name"]').value = 'discard'; [...document.querySelectorAll('#step-edit-content button')].find(b => b.textContent === 'Cancel').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && document.activeElement?.getAttribute('x-ref') === 'addStepButton'`,
	)
	open()
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
	b.wait(t, `!document.querySelector('#step-edit-dialog').open`)
	var list struct {
		Items []struct {
			ID     int64
			Name   string
			Script string `json:"script_body"`
		}
	}
	decodeStepLogTest(t, f.api(t, "GET", api, nil, 200), &list)
	if len(list.Items) != 0 {
		t.Fatal("canceling created a step")
	}
	open()
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	b.evaluate(
		t,
		`const form = document.querySelector('#step-edit-content form'); form.querySelector('[name="script_body"]').value = 'echo retained'; form.querySelector('[name="container_image"]').value = 'alpine:3.20'; form.querySelector('[type="submit"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-dialog').open && document.querySelector('#step-edit-content').textContent.includes('Name is required')`,
	)
	if string(
		b.evaluate(
			t,
			`document.querySelector('#step-edit-content [name="script_body"]').value === 'echo retained' && document.querySelector('#step-edit-content [name="container_image"]').value === 'alpine:3.20'`,
		),
	) != "true" {
		t.Fatal("validation discarded step fields")
	}
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	b.evaluate(
		t,
		`window.stepCreateSentinel = true; document.querySelector('#step-edit-content [name="name"]').value = 'modal-created-step'; document.querySelector('#step-edit-content [type="submit"]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && document.querySelector('#step-list').textContent.includes('modal-created-step') && document.activeElement?.getAttribute('x-ref') === 'addStepButton'`,
	)
	if string(
		b.evaluate(
			t,
			fmt.Sprintf(
				`location.pathname === %q && window.stepCreateSentinel === true`,
				path,
			),
		),
	) != "true" {
		t.Fatal("adding reloaded or navigated the document")
	}
	decodeStepLogTest(t, f.api(t, "GET", api, nil, 200), &list)
	if len(list.Items) != 1 || list.Items[0].Name != "modal-created-step" ||
		list.Items[0].Script != "echo retained" {
		t.Fatal("added step differs from API contract")
	}
	f.api(t, "GET", fmt.Sprintf("%s/%d", api, list.Items[0].ID), nil, 200)
}

func TestStepEditModalBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	api := fmt.Sprintf("/api/v1/projects/%d/steps", f.project.ID)
	type stepView struct {
		ID            int64
		Name          string
		ScriptBody    string   `json:"script_body"`
		VariableNames []string `json:"variable_names"`
	}
	var first, second stepView
	for i, step := range []*stepView{&first, &second} {
		decodeStepLogTest(t, f.api(t, "POST", api, map[string]any{
			"name": fmt.Sprintf("step-%d", i), "script_body": "echo original",
			"container_image": "alpine:3.20",
		}, 201), step)
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("%s/projects/%d/steps-page", f.baseURL, f.project.ID)
	b.navigateBackTest(t, path)
	open := func() {
		b.evaluate(
			t,
			`[...document.querySelectorAll('[data-step-action="edit"]')].find(el => el.getClientRects().length).click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('#step-edit-dialog')?.matches(':modal') && document.querySelector('#step-edit-content form')`,
		)
	}
	open()
	b.captureNavigation(t, "step-edit-modal", func() {
		b.wait(
			t,
			`document.getAnimations().every(a => a.playState !== 'running')`,
		)
		if string(b.evaluate(t, `(() => {
 const dialog = document.querySelector('#step-edit-dialog');
 const box = dialog.querySelector('.modal-box');
 box.scrollTop = box.scrollHeight;
 const save = dialog.querySelector('form button[type="submit"]').getBoundingClientRect();
 const cancel = [...dialog.querySelectorAll('form button')].find(el => el.textContent === 'Cancel').getBoundingClientRect();
 const header = dialog.querySelector('.page-header').getBoundingClientRect();
 const deletion = dialog.querySelector('[data-step-delete-section]');
 return dialog.matches(':modal') && dialog.querySelectorAll('form[data-step-edit-form]').length === 1 &&
 deletion === deletion.parentElement.lastElementChild && !document.querySelector('#step-list [data-step-action="delete"]') &&
 !document.querySelector('#step-list form') && box.scrollWidth <= box.clientWidth &&
 box.getBoundingClientRect().right <= innerWidth && save.bottom <= innerHeight &&
 save.top >= box.getBoundingClientRect().top && cancel.top === save.top &&
 Math.abs(cancel.right - header.right) < 2 && (innerWidth >= 768 || save.height >= 44);
})()`)) != "true" {
			b.screenshot(t, "step-modal-layout-failure")
			t.Fatal("step editor is not one contained modal form")
		}
	})
	// Cancel discards changes and returns focus to Edit.
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content input[name="name"]').value = 'discard'; [...document.querySelectorAll('#step-edit-content button')].find(el => el.textContent === 'Cancel').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && document.activeElement?.dataset.stepAction === 'edit'`,
	)
	var saved stepView
	decodeStepLogTest(
		t,
		f.api(t, "GET", fmt.Sprintf("%s/%d", api, first.ID), nil, 200),
		&saved,
	)
	if saved.Name != first.Name {
		t.Fatal("Cancel persisted an edit")
	}
	open()
	b.evaluate(
		t,
		`const input = document.querySelector('#step-edit-content input[name="name"]'); input.value = 'discard outside'; input.click(); true`,
	)
	if string(
		b.evaluate(
			t,
			`document.querySelector('#step-edit-dialog').matches(':modal')`,
		),
	) != "true" {
		t.Fatal("clicking inside closed the step modal")
	}
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		b.call(t, "Input.dispatchMouseEvent", map[string]any{
			"type": kind, "x": 1, "y": 100, "button": "left", "clickCount": 1,
		}, &struct{}{})
	}
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && document.activeElement?.dataset.stepAction === 'edit'`,
	)
	decodeStepLogTest(
		t,
		f.api(t, "GET", fmt.Sprintf("%s/%d", api, first.ID), nil, 200),
		&saved,
	)
	if saved.Name != first.Name {
		t.Fatal("outside click persisted an edit")
	}
	open()
	// A server validation error keeps the modal and submitted values.
	b.evaluate(
		t,
		`const form = document.querySelector('#step-edit-content form'); form.querySelector('[name="name"]').value = ''; form.querySelector('[name="script_body"]').value = 'echo edited'; form.querySelector('[type="submit"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-dialog').open && document.querySelector('#step-edit-content').textContent.includes('Name is required')`,
	)
	if string(
		b.evaluate(
			t,
			`document.querySelector('#step-edit-content [name="script_body"]').value === 'echo edited'`,
		),
	) != "true" {
		t.Fatal("validation discarded the script")
	}
	b.captureNavigation(t, "step-edit-modal-validation")
	b.evaluate(
		t,
		`const target = document.querySelector('#step-edit-content [name="execution_target"]'); target.value = 'agent'; target.dispatchEvent(new Event('change', {bubbles: true})); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-content [name="container_image"]').disabled`,
	)
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content [name="name"]').value = 'saved-step'; document.querySelector('#step-edit-content [name="variable_names"]').value = 'REMOTE_TOKEN'; document.querySelector('#step-edit-content [type="submit"]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && document.querySelector('#step-list').textContent.includes('saved-step')`,
	)
	decodeStepLogTest(
		t,
		f.api(t, "GET", fmt.Sprintf("%s/%d", api, first.ID), nil, 200),
		&saved,
	)
	if saved.Name != "saved-step" || saved.ScriptBody != "echo edited" {
		t.Fatal("Save did not persist the edited step")
	}
	if len(saved.VariableNames) != 1 ||
		saved.VariableNames[0] != "REMOTE_TOKEN" {
		t.Fatalf(
			"Save did not persist variable restrictions: %v",
			saved.VariableNames,
		)
	}
	decodeStepLogTest(
		t,
		f.api(t, "GET", fmt.Sprintf("%s/%d", api, second.ID), nil, 200),
		&saved,
	)
	if saved.Name != second.Name || saved.ScriptBody != second.ScriptBody {
		t.Fatal("Save changed the other step")
	}
	open()
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown", "key": "Escape", "code": "Escape",
		"windowsVirtualKeyCode": 27,
	}, &struct{}{})
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyUp", "key": "Escape", "code": "Escape",
		"windowsVirtualKeyCode": 27,
	}, &struct{}{})
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && !document.querySelector('#step-edit-content form') && document.activeElement?.dataset.stepAction === 'edit'`,
	)
	open()
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	b.evaluate(
		t,
		`window.confirm = () => false; document.querySelector('#step-edit-content [data-step-action="delete"]').click(); true`,
	)
	f.api(t, "GET", fmt.Sprintf("%s/%d", api, first.ID), nil, 200)
	if string(
		b.evaluate(
			t,
			`document.querySelector('#step-edit-dialog').matches(':modal')`,
		),
	) != "true" {
		t.Fatal("canceling deletion closed the editor")
	}
	b.evaluate(
		t,
		`window.confirm = () => true; document.querySelector('#step-edit-content [data-step-action="delete"]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && !document.querySelector('#step-list').textContent.includes('saved-step') && document.activeElement?.dataset.stepAction === 'edit'`,
	)
	f.api(t, "GET", fmt.Sprintf("%s/%d", api, first.ID), nil, 404)
	f.api(t, "GET", fmt.Sprintf("%s/%d", api, second.ID), nil, 200)
	open()
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content [data-step-action="delete"]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && document.activeElement?.getAttribute('x-ref') === 'addStepButton'`,
	)
	f.api(t, "GET", fmt.Sprintf("%s/%d", api, second.ID), nil, 404)
}

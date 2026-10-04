//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestStepEditModalBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	api := fmt.Sprintf("/api/v1/projects/%d/steps", f.project.ID)
	type stepView struct {
		ID         int64
		Name       string
		ScriptBody string `json:"script_body"`
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
 const save = dialog.querySelector('form button[type="submit"]').getBoundingClientRect();
 return dialog.matches(':modal') && dialog.querySelectorAll('form[data-step-edit-form]').length === 1 &&
 !document.querySelector('#step-list form') && box.scrollWidth <= box.clientWidth &&
 box.getBoundingClientRect().right <= innerWidth && save.bottom <= innerHeight &&
 save.top >= box.getBoundingClientRect().top && (innerWidth >= 768 || save.height >= 44);
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
		`document.querySelector('#step-edit-content [name="name"]').value = 'saved-step'; document.querySelector('#step-edit-content [type="submit"]').click(); true`,
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
}

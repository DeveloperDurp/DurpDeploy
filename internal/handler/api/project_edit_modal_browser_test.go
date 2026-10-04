//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestProjectEditModalBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(
		t,
		"POST",
		"/api/v1/projects",
		map[string]string{"name": "duplicate-project"},
		201,
	)
	member := seedAPIUser(t, f.h.repo, "modal-member@example.test", "deployer")
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	api := "/api/v1" + path
	b.navigateBackTest(t, f.baseURL+path)
	open := func() {
		b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
		b.evaluate(
			t,
			`document.querySelector('#project-detail .page-header a[hx-get]').click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('#project-edit-dialog')?.matches(':modal') && document.querySelector('#project-edit-content [data-project-form]')`,
		)
	}
	open()
	b.captureNavigation(t, "project-edit-modal", func() {
		b.wait(
			t,
			`document.getAnimations().every(a => a.playState !== 'running')`,
		)
		if string(b.evaluate(t, `(() => {
 const dialog = document.querySelector('#project-edit-dialog');
 const box = dialog.querySelector('.modal-box');
 const form = dialog.querySelector('[data-project-form]');
 const deletion = form.querySelector('button[hx-delete]').parentElement;
 box.scrollTop = box.scrollHeight;
 const save = dialog.querySelector('button[form]').getBoundingClientRect();
 return dialog.matches(':modal') && box.scrollWidth <= box.clientWidth &&
 box.getBoundingClientRect().right <= innerWidth && deletion === form.lastElementChild &&
 save.top >= box.getBoundingClientRect().top && save.bottom <= innerHeight &&
 (innerWidth >= 768 || save.height >= 44);
})()`)) != "true" {
			t.Fatal("project modal clips fields, hides Save, or moves Delete")
		}
	})
	// Back closes without saving, and restores focus to Edit.
	b.evaluate(
		t,
		`document.querySelector('#project-edit-content input[name="name"]').value = 'discard'; document.querySelector('#project-edit-content a[x-data="backNavigation"]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#project-edit-dialog').open && document.activeElement?.hasAttribute('hx-get')`,
	)
	var project struct{ Name, Description string }
	decodeStepLogTest(t, f.api(t, "GET", api, nil, 200), &project)
	if project.Name != f.project.Name {
		t.Fatal("Back saved unsaved project fields")
	}
	open()
	b.call(
		t,
		"Input.dispatchKeyEvent",
		map[string]any{
			"type":                  "keyDown",
			"key":                   "Escape",
			"code":                  "Escape",
			"windowsVirtualKeyCode": 27,
		},
		&struct{}{},
	)
	b.wait(t, `!document.querySelector('#project-edit-dialog').open`)
	open()
	b.evaluate(
		t,
		`const input = document.querySelector('#project-edit-content input[name="name"]'); input.value = 'discard outside'; input.click(); true`,
	)
	if string(
		b.evaluate(
			t,
			`document.querySelector('#project-edit-dialog').matches(':modal')`,
		),
	) != "true" {
		t.Fatal("clicking inside closed the project modal")
	}
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		b.call(t, "Input.dispatchMouseEvent", map[string]any{
			"type": kind, "x": 1, "y": 100, "button": "left", "clickCount": 1,
		}, &struct{}{})
	}
	b.wait(
		t,
		`!document.querySelector('#project-edit-dialog').open && document.activeElement?.hasAttribute('hx-get')`,
	)
	decodeStepLogTest(t, f.api(t, "GET", api, nil, 200), &project)
	if project.Name != f.project.Name {
		t.Fatal("outside click persisted a project edit")
	}
	open()
	// Server validation remains in the modal with all submitted values.
	b.evaluate(
		t,
		`document.querySelector('#project-edit-content input[name="name"]').value = 'duplicate-project'; document.querySelector('#project-edit-content textarea').value = 'retain description'; document.querySelector('#project-edit-content button[form]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').open && document.querySelector('#project-edit-content .text-error')?.textContent.includes('already exists')`,
	)
	if string(
		b.evaluate(
			t,
			`document.querySelector('#project-edit-content textarea').value === 'retain description' && document.querySelector('#project-detail > .page-header h1').textContent !== 'duplicate-project'`,
		),
	) != "true" {
		t.Fatal("validation lost fields or replaced the underlying project")
	}
	// Membership changes refresh inside the dialog and persist through the API.
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('#project-edit-content select[name="user_id"]').value = %q; document.querySelector('#project-edit-content form[hx-post] button[type="submit"]').click(); true`,
			fmt.Sprint(member.ID),
		),
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').open && document.querySelector('#project-edit-content tbody').textContent.includes('modal-member@example.test')`,
	)
	var members struct {
		Items []struct {
			UserID int64 `json:"user_id"`
		}
	}
	decodeStepLogTest(t, f.api(t, "GET", api+"/members", nil, 200), &members)
	if len(members.Items) != 1 || members.Items[0].UserID != member.ID {
		t.Fatal("modal membership change did not persist")
	}
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	b.evaluate(
		t,
		fmt.Sprintf(
			`window.confirm = () => true; document.querySelector('#project-edit-content button[hx-delete=%q]').click(); true`,
			path+"/members/"+fmt.Sprint(member.ID),
		),
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').open && !document.querySelector('#project-edit-content tbody').textContent.includes('modal-member@example.test')`,
	)
	// Save refreshes detail without changing the project URL or reloading bundles.
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	b.evaluate(
		t,
		`window.modalSaveSentinel = true; document.querySelector('#project-edit-content input[name="name"]').value = 'modal-saved-project'; document.querySelector('#project-edit-content button[form]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#project-edit-dialog').open && document.querySelector('#project-detail .page-header h1').textContent === 'modal-saved-project'`,
	)
	if string(
		b.evaluate(
			t,
			fmt.Sprintf(
				`location.pathname === %q && window.modalSaveSentinel === true`,
				path,
			),
		),
	) != "true" {
		t.Fatal("modal Save navigated or reloaded the document")
	}
	decodeStepLogTest(t, f.api(t, "GET", api, nil, 200), &project)
	if project.Name != "modal-saved-project" {
		t.Fatal("modal Save did not persist through the API")
	}
	open()
	b.evaluate(
		t,
		`window.confirm = () => false; document.querySelector('#project-edit-content [data-project-form] > div:last-child button').click(); true`,
	)
	f.api(t, "GET", api, nil, 200)
	b.evaluate(
		t,
		`window.confirm = () => true; document.querySelector('#project-edit-content [data-project-form] > div:last-child button').click(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/projects' && document.querySelector('main h1')?.textContent === 'Projects'`,
	)
	f.api(t, "GET", api, nil, 404)
}

//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestAlpineEditFormsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	base := fmt.Sprintf("/api/v1/projects/%d", f.project.ID)
	f.api(t, "POST", base+"/variables", map[string]any{
		"name": "EDITOR_VALUE", "value": "initial",
	}, 201)
	release := seedRelease(t, f.h.repo, f.project.ID)
	f.api(t, "POST", base+"/schedules", map[string]any{
		"release_id": release.ID, "environment_id": f.environment.ID,
		"cron": "0 4 * * *",
	}, 201)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, page := range []struct{ name, selector, action string }{
		{"variables", "[data-desktop-variable-edit], [data-mobile-variable-edit]",
			"[data-variable-action=edit]"},
		{"schedules", "[data-desktop-schedule-edit], [data-mobile-schedule-edit]",
			"[data-schedule-action=edit]"},
	} {
		address := fmt.Sprintf("%s/projects/%d/%s",
			f.baseURL, f.project.ID, page.name)
		// Given a full load before Alpine can initialize.
		b.call(t, "Emulation.setScriptExecutionDisabled",
			map[string]bool{"value": true}, &struct{}{})
		b.call(t, "Page.navigate",
			map[string]string{"url": address}, &struct{}{})
		b.wait(t, fmt.Sprintf(
			`location.href === %q && document.readyState === 'complete'`,
			address,
		))
		// Both desktop and mobile edit forms must already be hidden.
		if string(b.evaluate(t, fmt.Sprintf(
			`!window.Alpine && document.querySelectorAll(%q).length === 2 && [...document.querySelectorAll(%q)].every(e => getComputedStyle(e).display === 'none')`,
			page.selector,
			page.selector,
		))) != "true" {
			t.Fatal("edit form visible before Alpine starts: " + page.name)
		}
		b.call(t, "Emulation.setScriptExecutionDisabled",
			map[string]bool{"value": false}, &struct{}{})
		b.navigateBackTest(t, address)
		checkScheduleLayout := func() {
			if page.name == "schedules" &&
				string(
					b.evaluate(
						t,
						`(() => { const table = document.querySelector('#schedules-content table'); return !table.getClientRects().length || [...table.querySelectorAll('tbody td:not(.truncate)')].every(cell => cell.scrollWidth <= cell.clientWidth); })()`,
					),
				) != "true" {
				t.Fatal("schedule cell contents overlap adjacent columns")
			}
		}
		b.captureNavigation(t, page.name+"-closed", checkScheduleLayout)
		b.evaluate(t, fmt.Sprintf(
			`[...document.querySelectorAll(%q)].forEach(e => e.click()); true`,
			page.action,
		))
		b.wait(t, fmt.Sprintf(
			`[...document.querySelectorAll(%q)].some(e => e.getClientRects().length > 0)`,
			page.selector,
		))
		b.captureNavigation(t, page.name+"-editing", checkScheduleLayout)
		b.evaluate(t, fmt.Sprintf(
			`[...document.querySelectorAll(%q)].forEach(e => e.querySelector('button[type=button]').click()); true`,
			page.selector,
		))
		b.wait(t, fmt.Sprintf(
			`[...document.querySelectorAll(%q)].every(e => getComputedStyle(e).display === 'none')`,
			page.selector,
		))
	}
}

func TestAlpineDialogsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	path := fmt.Sprintf("/projects/%d/steps-page", f.project.ID)
	f.api(t, "POST", fmt.Sprintf(
		"/api/v1/projects/%d/steps", f.project.ID,
	), map[string]any{
		"name": "editor", "script_body": "printf editor",
		"container_image": "alpine:3.20",
	}, 201)
	event, err := f.h.repo.Queries.CreateNotificationEvent(t.Context(),
		db.CreateNotificationEventParams{
			EventType: "deployment.succeeded",
			Message:   "Deployment completed", Results: "[]",
		})
	if err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, f.baseURL+path)
	for _, control := range []struct{ name, selector string }{
		{"script-add", "button[hx-target='#add-step-form']"},
		{"script-edit", "[data-step-action=edit]"},
	} {
		b.captureNavigation(t, control.name+"-dialog", func() {
			b.navigateBackTest(t, f.baseURL+path)
			b.evaluate(t, fmt.Sprintf(
				`[...document.querySelectorAll(%q)].find(e => e.getClientRects().length).click(); true`,
				control.selector,
			))
			b.wait(
				t,
				`!!document.querySelector('[x-data="stepEditor"]') && [...document.querySelectorAll('[x-data="stepEditor"] button')].some(e => e.getClientRects().length)`,
			)
			b.evaluate(
				t,
				`[...document.querySelectorAll('[x-data="stepEditor"] button')].find(e => e.textContent === 'Fullscreen' && e.getClientRects().length).click(); true`,
			)
			b.wait(t, `!!document.querySelector('dialog[open]')`)
			b.assertAlpineDialogName(t, "Script Body")
		})
		b.call(t, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyDown", "key": "Escape", "code": "Escape",
			"windowsVirtualKeyCode": 27,
		}, &struct{}{})
		b.call(t, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyUp", "key": "Escape", "code": "Escape",
			"windowsVirtualKeyCode": 27,
		}, &struct{}{})
		b.wait(t, `!document.querySelector('dialog[open]')`)
		b.navigateBackTest(t, f.baseURL+path)
	}
	b.navigateBackTest(t, f.baseURL+"/admin/notifications")
	b.evaluate(t, fmt.Sprintf(
		`[...document.querySelectorAll('[data-modal-target="notification_modal_%d"]')].find(e => e.getClientRects().length).click(); true`,
		event.ID,
	))
	b.wait(t, `!!document.querySelector('dialog[open]')`)
	b.assertAlpineDialogName(t, "Notification Event")
	b.captureNavigation(t, "notification-dialog")
	b.evaluate(
		t,
		`document.querySelector('dialog[open] button.btn').click(); true`,
	)
	b.wait(t, `!document.querySelector('dialog[open]')`)
}

func (b *packageBrowser) assertAlpineDialogName(t *testing.T, want string) {
	t.Helper()
	b.wait(
		t,
		`document.querySelector('dialog[open]')?.getClientRects().length > 0 && !document.querySelector('dialog[open]').hasAttribute('x-cloak')`,
	)
	b.call(t, "Accessibility.enable", struct{}{}, &struct{}{})
	var document struct {
		Root struct {
			NodeID int `json:"nodeId"`
		} `json:"root"`
	}
	b.call(t, "DOM.getDocument", struct{}{}, &document)
	var selected struct {
		NodeID int `json:"nodeId"`
	}
	b.call(t, "DOM.querySelector", map[string]any{
		"nodeId": document.Root.NodeID, "selector": "dialog[open]",
	}, &selected)
	var tree struct {
		Nodes []struct {
			Role struct {
				Value string `json:"value"`
			} `json:"role"`
			Name struct {
				Value string `json:"value"`
			} `json:"name"`
		} `json:"nodes"`
	}
	b.call(t, "Accessibility.getPartialAXTree", map[string]any{
		"nodeId": selected.NodeID, "fetchRelatives": false,
	}, &tree)
	if len(tree.Nodes) != 1 || tree.Nodes[0].Role.Value != "dialog" ||
		tree.Nodes[0].Name.Value != want {
		encoded, err := json.Marshal(tree)
		if err != nil {
			t.Fatal(err)
		}
		t.Fatalf("dialog accessibility = %s, want %q", encoded, want)
	}
}

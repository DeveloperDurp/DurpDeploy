//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestStepFormActionsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	api := fmt.Sprintf("/api/v1/projects/%d/steps", f.project.ID)
	type stepView struct {
		ID             int64
		Name           string
		TimeoutSeconds int64 `json:"timeout_seconds"`
		MaxRetries     int64 `json:"max_retries"`
	}
	var step stepView
	decodeStepLogTest(t, f.api(t, "POST", api, map[string]any{
		"name": "reachable-actions", "script_body": "echo original",
		"container_image": "harbor.durp.info/dockerhub/library/alpine:3.20",
		"timeout_seconds": 30, "max_retries": 1,
	}, 201), &step)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, size := range []struct{ width, height int }{
		{375, 844}, {375, 420}, {1280, 900},
	} {
		t.Run(
			fmt.Sprintf("%dx%d", size.width, size.height),
			func(t *testing.T) {
				b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
					"width": size.width, "height": size.height,
					"deviceScaleFactor": 1, "mobile": false,
				}, &struct{}{})
				b.navigateBackTest(t, fmt.Sprintf(
					"%s/projects/%d/steps-page", f.baseURL, f.project.ID,
				))
				for _, opener := range []string{
					"[data-step-action=edit]", "[x-ref=addStepButton]",
				} {
					add := opener == "[x-ref=addStepButton]"
					b.evaluate(t, fmt.Sprintf(
						`[...document.querySelectorAll(%q)].find(e => e.getClientRects().length).click(); true`,
						opener,
					))
					b.wait(
						t,
						`document.querySelector('#step-edit-dialog')?.matches(':modal') && document.querySelector('#step-edit-content form') && !document.querySelector('.htmx-request, .htmx-settling') && document.getAnimations().every(a => a.playState !== 'running')`,
					)
					checkStepFormActions(t, b)
					b.screenshot(
						t,
						fmt.Sprintf(
							"step-actions-%dx%d-add-%t",
							size.width,
							size.height,
							add,
						),
					)
					// Cancel discards a lower-field edit without scrolling back.
					b.evaluate(
						t,
						`document.querySelector('#step-edit-content [name=timeout_seconds]').value = '61'; [...document.querySelectorAll('#step-edit-content .page-header button')].find(e => e.textContent === 'Cancel').click(); true`,
					)
					b.wait(
						t,
						`!document.querySelector('#step-edit-dialog').open && !document.querySelector('#step-edit-content form')`,
					)
					var saved stepView
					decodeStepLogTest(
						t,
						f.api(
							t,
							"GET",
							fmt.Sprintf("%s/%d", api, step.ID),
							nil,
							200,
						),
						&saved,
					)
					if saved.TimeoutSeconds != 30 || saved.MaxRetries != 1 {
						t.Fatal("Cancel persisted a lower-field edit")
					}
				}
			},
		)
	}
	// Validation retains lower fields, and the visible Save persists them.
	b.evaluate(
		t,
		`[...document.querySelectorAll('[data-step-action=edit]')].find(e => e.getClientRects().length).click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-content form') && !document.querySelector('.htmx-request, .htmx-settling')`,
	)
	b.evaluate(
		t,
		`const form = document.querySelector('#step-edit-content form'); form.querySelector('[name=name]').value = ''; form.querySelector('[name=timeout_seconds]').value = '62'; form.querySelector('[name=max_retries]').value = '2'; form.querySelector('[type=submit]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#step-edit-content').textContent.includes('Name is required') && !document.querySelector('.htmx-request, .htmx-settling')`,
	)
	checkStepFormActions(t, b)
	if string(
		b.evaluate(
			t,
			`document.querySelector('#step-edit-content [name=timeout_seconds]').value === '62' && document.querySelector('#step-edit-content [name=max_retries]').value === '2'`,
		),
	) != "true" {
		t.Fatal("validation discarded lower fields")
	}
	b.evaluate(
		t,
		`document.querySelector('#step-edit-content [name=name]').value = 'saved-actions'; document.querySelector('#step-edit-content [type=submit]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#step-edit-dialog').open && document.querySelector('#step-list').textContent.includes('saved-actions')`,
	)
	var saved stepView
	decodeStepLogTest(
		t,
		f.api(t, "GET", fmt.Sprintf("%s/%d", api, step.ID), nil, 200),
		&saved,
	)
	if saved.Name != "saved-actions" || saved.TimeoutSeconds != 62 ||
		saved.MaxRetries != 2 {
		t.Fatal("Save differs from the public API step contract")
	}
}

func checkStepFormActions(t *testing.T, b *packageBrowser) {
	t.Helper()
	if string(b.evaluate(t, `(() => {
 const form = document.querySelector('#step-edit-content form');
 const field = form.querySelector('[name=container_image]');
 let pane = field.parentElement;
 while (pane && !(pane.scrollHeight > pane.clientHeight && /auto|scroll/.test(getComputedStyle(pane).overflowY))) pane = pane.parentElement;
 if (!pane) return false;
 const header = form.querySelector('.page-header');
 const buttons = [...header.querySelectorAll('button')];
 const hit = e => {const r = e.getBoundingClientRect(); return r.top >= 0 && r.bottom <= innerHeight && e.contains(document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2));};
 for (const fraction of [0, 0.25, 0.5, 0.75, 1]) {
  pane.scrollTop = (pane.scrollHeight - pane.clientHeight) * fraction;
  if (!buttons.every(hit)) return false;
 }
 field.focus();
 pane.scrollTop += field.getBoundingClientRect().top - pane.getBoundingClientRect().top - 8;
 const r = field.getBoundingClientRect();
 return document.activeElement === field && hit(field) &&
 r.top >= header.getBoundingClientRect().bottom && buttons.every(hit) &&
 document.documentElement.scrollWidth <= innerWidth;
})()`)) != "true" {
		b.screenshot(t, "step-actions-failure")
		t.Fatal(
			"step actions obscure focus or become unreachable while scrolling",
		)
	}
}

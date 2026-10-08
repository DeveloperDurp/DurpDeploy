//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestMobileFormFocusBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	project := fmt.Sprintf("/projects/%d", f.project.ID)
	f.api(t, "POST", "/api/v1"+project+"/steps", map[string]string{
		"name": "focus-step", "script_body": "echo focus",
		"container_image": "alpine:3.20",
	}, 201)
	var book struct{ Runbook struct{ ID int64 } }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1"+project+"/runbooks",
		map[string]any{
			"name": "focus-runbook", "steps": []map[string]string{{
				"name": "focus", "script_body": "echo focus",
				"container_image": "alpine:3.20",
			}},
		}, 201), &book)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	cases := []struct{ name, path, opener, field string }{
		{
			"project edit",
			project,
			".page-header a[hx-get]",
			"#project-edit-content input[name=name]",
		},
		{
			"step add",
			project + "/steps-page",
			"[x-ref=addStepButton]",
			"#step-edit-content input[name=name]",
		},
		{
			"step edit",
			project + "/steps-page",
			"[data-step-action=edit]",
			"#step-edit-content input[name=name]",
		},
		{
			"runbook step",
			fmt.Sprintf("%s/runbooks/%d/edit", project, book.Runbook.ID),
			"#runbook-version-form button.btn-secondary",
			"dialog input[name=step_name]",
		},
	}
	for _, resource := range []string{"projects", "environments", "lifecycles", "templates"} {
		cases = append(cases, struct{ name, path, opener, field string }{
			resource + " create", "/" + resource,
			fmt.Sprintf(".page-header a[href='/%s/new']", resource),
			"#project-edit-content input[name=name]",
		})
	}
	for _, touch := range []bool{true, false} {
		width := 1280
		if touch {
			width = 375
		}
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1,
			"mobile": touch,
		}, &struct{}{})
		b.call(t, "Emulation.setTouchEmulationEnabled", map[string]any{
			"enabled": touch, "maxTouchPoints": 1,
		}, &struct{}{})
		for _, scenario := range cases {
			t.Run(
				fmt.Sprintf("%s/touch=%t", scenario.name, touch),
				func(t *testing.T) {
					b.navigateBackTest(t, f.baseURL+scenario.path)
					b.evaluate(t, fmt.Sprintf(
						`[...document.querySelectorAll(%q)].find(e => e.getClientRects().length).click(); true`,
						scenario.opener,
					))
					b.wait(t, fmt.Sprintf(
						`document.querySelector(%q)?.closest('dialog').matches(':modal') && !document.querySelector('.htmx-request, .htmx-settling')`,
						scenario.field,
					))
					if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const field = document.querySelector(%q);
 return %t ? !document.activeElement.matches('input, textarea, select') &&
 field.closest('dialog').contains(document.activeElement) : document.activeElement === field;
})()`, scenario.field, touch))) != "true" {
						t.Fatal(
							"automatic field focus does not match the input device",
						)
					}
					if touch && scenario.name == "project edit" {
						b.wait(
							t,
							`document.getAnimations().every(a => a.playState !== 'running')`,
						)
						b.screenshot(t, "mobile-project-no-autofocus")
					}
					// Deliberate field interaction remains available on touch devices.
					b.evaluate(
						t,
						fmt.Sprintf(
							`document.querySelector(%q).focus(); true`,
							scenario.field,
						),
					)
					b.wait(
						t,
						fmt.Sprintf(
							`document.activeElement === document.querySelector(%q)`,
							scenario.field,
						),
					)
					if scenario.name == "step edit" {
						b.evaluate(
							t,
							`[...document.querySelectorAll('#step-edit-content button')].find(e => e.textContent === 'Fullscreen').click(); true`,
						)
						b.wait(
							t,
							`document.querySelector('[x-ref=modal]').matches(':modal') && document.querySelector('[x-ref=modalTextarea]').getClientRects().length > 0`,
						)
						if string(b.evaluate(t, fmt.Sprintf(
							`%t ? document.activeElement.textContent === 'Close' : document.activeElement === document.querySelector('[x-ref=modalTextarea]')`,
							touch,
						))) != "true" {
							t.Fatalf(
								"fullscreen editor focus mismatch: %s",
								b.evaluate(
									t,
									`JSON.stringify({tag: document.activeElement.tagName, ref: document.activeElement.getAttribute('x-ref')})`,
								),
							)
						}
						if touch {
							b.screenshot(t, "mobile-fullscreen-no-autofocus")
						}
					}
				},
			)
		}
		b.navigateBackTest(t, f.baseURL+project+"/variables")
		for _, refresh := range []bool{false, true} {
			if refresh {
				b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('#variables-content form[hx-post]');
 form.querySelector('[name=name]').value = %q;
 form.querySelector('[name=value]').value = 'focus value';
 form.querySelector('[type=submit]').click();
 return true;
})()`, fmt.Sprintf("FOCUS_%t", touch)))
				b.wait(
					t,
					`!document.querySelector('.htmx-request, .htmx-settling') && document.querySelector('#variables-content form [name=name]').value === ''`,
				)
			}
			if string(b.evaluate(t, fmt.Sprintf(
				`%t ? !document.activeElement.matches('input, textarea, select') : document.activeElement === document.querySelector('#variables-content form [name=name]')`,
				touch,
			))) != "true" {
				t.Fatalf(
					"variable form autofocus mismatch: touch=%t refresh=%t",
					touch,
					refresh,
				)
			}
		}
	}
}

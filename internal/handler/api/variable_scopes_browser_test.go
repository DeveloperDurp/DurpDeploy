//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestVariableScopesBeforeDeploymentBrowserE2E(t *testing.T) {
	// Given: fresh projects, eligible environments, and no deployment history.
	f := &artifactE2E{
		baseURL: strings.TrimRight(os.Getenv("DURPDEPLOY_LIVE_BASE"), "/"),
		token:   os.Getenv("DURPDEPLOY_LIVE_API_TOKEN"),
		session: os.Getenv("DURPDEPLOY_LIVE_SESSION"),
		client:  &http.Client{Timeout: time.Minute},
	}
	if f.baseURL == "" {
		f = newArtifactE2E(t)
	}
	var environments struct{ Items []struct{ ID int64 } }
	decodeStepLogTest(t, f.api(t, "GET",
		"/api/v1/environments?limit=1000", nil, 200), &environments)
	for len(environments.Items) < 2 {
		f.api(
			t,
			"POST",
			"/api/v1/environments",
			map[string]string{
				"name": "variable-outside-" + uuid.NewString(),
			},
			201,
		)
		decodeStepLogTest(t, f.api(t, "GET",
			"/api/v1/environments?limit=1000", nil, 200), &environments)
	}
	eligible, outside := environments.Items[0].ID, environments.Items[1].ID
	var lifecycle struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "variable-scopes-" + uuid.NewString()},
		201), &lifecycle)
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/stages",
		lifecycle.ID), map[string]int64{
		"environment_id": eligible, "sort_order": 1,
	}, 201)
	b := startPackageBrowser(t)
	b.call(t, "Page.bringToFront", struct{}{}, &struct{}{})
	b.call(t, "Security.setIgnoreCertificateErrors",
		map[string]bool{"ignore": true}, &struct{}{})
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, width := range []int{1280, 375} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1,
			"mobile": width == 375,
		}, &struct{}{})
		for _, bound := range []bool{true, false} {
			scope := variableLifecycleScope{
				eligible: eligible,
				outside:  outside,
				bound:    bound,
			}
			for _, boosted := range []bool{false, true} {
				t.Run(fmt.Sprintf("width=%d/bound=%t/htmx=%t",
					width, bound, boosted), func(t *testing.T) {
					name := "variable-before-deploy-" + uuid.NewString()
					request := map[string]any{
						"name":        name,
						"description": "Managed by make e2e-test. Variable scopes before deployment.",
					}
					if bound {
						request["lifecycle_id"] = lifecycle.ID
					}
					var project struct{ ID int64 }
					decodeStepLogTest(t, f.api(t, "POST",
						"/api/v1/projects", request, 201), &project)
					path := fmt.Sprintf("/projects/%d", project.ID)
					apiPath := "/api/v1" + path
					var editable struct{ ID int64 }
					decodeStepLogTest(t, f.api(
						t,
						"POST",
						apiPath+"/variables",
						map[string]string{
							"name":  "EDIT_ME",
							"value": "initial",
						},
						201,
					), &editable)
					f.api(
						t,
						"POST",
						apiPath+"/variables",
						map[string]string{
							"name":  "DEFAULT",
							"value": "global",
						},
						201,
					)
					b.navigateBackTest(t, f.baseURL+path+"/variables")
					if boosted {
						b.navigateBackTest(t, f.baseURL+path)
						b.evaluate(t, `window.variableScopeNavigation = true;
document.querySelector('.page-header button[aria-controls="project-menu-dialog"]').click(); true`)
						b.wait(
							t,
							`document.querySelector('#project-menu-dialog').matches(':modal')`,
						)
						b.evaluate(t, fmt.Sprintf(
							`document.querySelector('#project-menu-dialog a[href=%q]').click(); true`,
							path+"/variables",
						))
						b.waitBackTestURL(t, f.baseURL+path+"/variables")
						if string(b.evaluate(
							t,
							`window.variableScopeNavigation === true`,
						)) != "true" {
							t.Fatal(
								"Variables navigation reloaded the document",
							)
						}
					}
					// When: select scopes in new, edit, and override forms.
					submit := func(selector, variableName, value string) {
						t.Helper()
						b.wait(
							t,
							`!document.querySelector('.htmx-request, .htmx-settling')`,
						)
						b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector(%q);
 form.elements.namedItem('name').value = %q;
 form.elements.namedItem('value').value = %q;
 return true;
})()`, selector, variableName, value))
						b.selectVariableScope(t, selector, scope.eligible)
						b.evaluate(t, fmt.Sprintf(
							`document.querySelector(%q).requestSubmit(); true`,
							selector,
						))
						b.wait(t, fmt.Sprintf(
							`document.querySelector('#variables-content').textContent.includes(%q) && !document.querySelector('.htmx-request, .htmx-settling')`,
							value,
						))
					}
					submit(
						"#variables-content form[hx-post]",
						"NEW",
						"new-scoped",
					)
					edit := fmt.Sprintf(
						"#variable-row-%d [data-variable-action=edit]",
						editable.ID,
					)
					form := fmt.Sprintf(
						"#variable-row-%d [data-desktop-variable-edit] form",
						editable.ID,
					)
					if width == 375 {
						edit = fmt.Sprintf(
							"[data-mobile-variable='%d'] [data-variable-action=edit]",
							editable.ID,
						)
						form = fmt.Sprintf(
							"[data-mobile-variable='%d'] form",
							editable.ID,
						)
					}
					b.evaluate(t, fmt.Sprintf(
						`document.querySelector(%q).click(); true`, edit))
					b.wait(t, fmt.Sprintf(
						`!!document.querySelector(%q)?.getClientRects().length`,
						form,
					))
					submit(form, "EDIT_ME", "edit-scoped")
					b.evaluate(t, `(() => {
 [...document.querySelectorAll('[data-override-for="DEFAULT"]')]
 .find(e => e.getClientRects().length).click(); return true;
})()`)
					submit(
						"#variables-content form[hx-post]",
						"DEFAULT",
						"override-scoped",
					)
					// Then: public API writes agree and the first snapshot retains scope.
					f.checkVariableScopeSnapshot(t, apiPath, scope)
					b.screenshot(t, fmt.Sprintf("variable-scopes-%d-%t-%t",
						width, bound, boosted))
					t.Logf(
						"Retained variable fixture: %s%s/variables",
						f.baseURL,
						path,
					)
				})
			}
		}
	}
}

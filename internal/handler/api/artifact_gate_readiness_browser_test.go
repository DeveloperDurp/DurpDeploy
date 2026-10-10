//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/events"
)

func TestArtifactGateReadinessBrowserE2E(t *testing.T) {
	// Given: the plan exists, but real container cleanup has not finished.
	// Start Chromium before holding cleanup, which has a bounded timeout.
	browser := startPackageBrowser(t)
	f, deployment, release := publishingGate(t)
	var cookie struct{ Success bool }
	browser.call(t, "Network.setCookie", map[string]any{
		"name": "session", "value": f.session, "url": f.baseURL,
		"httpOnly": true,
	}, &cookie)
	if !cookie.Success {
		t.Fatal("session cookie rejected")
	}
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	browser.call(t, "Page.navigate", map[string]string{
		"url": f.baseURL + path,
	}, &struct{}{})
	browser.wait(
		t,
		`document.querySelector('[data-artifact-publishing]') && window.htmx`,
	)
	for _, width := range []int{375, 768, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1,
			"mobile": false,
		}, &struct{}{})
		if string(
			browser.evaluate(
				t,
				`!!document.querySelector('form[action$="/approve"], form[action$="/reject"]')`,
			),
		) != "false" {
			t.Fatal("browser offered a decision before cleanup")
		}
		browser.screenshot(t, fmt.Sprintf("artifact-publishing-%d", width))
	}
	// When: cleanup is released without navigating or reloading the page.
	release()
	f.completion(t, deployment.ID, events.ArtifactAwaitingApproval)
	browser.wait(
		t,
		`document.querySelector('form[action$="/approve"]') && document.querySelector('form[action$="/reject"]') && !document.querySelector('[data-artifact-publishing]')`,
	)
	// Then: polling makes the current decision available automatically.
	if string(
		browser.evaluate(t, `location.pathname`),
	) != fmt.Sprintf(
		"%q",
		path,
	) {
		t.Fatal("readiness navigated away from the deployment")
	}
	for _, state := range []string{"ready", "conflict"} {
		if state == "conflict" {
			// A stale open form must recover to fresh gate state and an alert.
			browser.evaluate(
				t,
				`document.querySelector('form[action$="/approve"] [name="revision"]').value='99'`,
			)
			browser.evaluate(
				t,
				`document.querySelector('form[action$="/approve"] button').click()`,
			)
			browser.wait(
				t,
				`document.querySelector('[data-artifact-decision-warning]') && document.querySelector('form[action$="/approve"] [name="revision"]')?.value==='1'`,
			)
			if string(
				browser.evaluate(t, `location.pathname`),
			) != fmt.Sprintf(
				"%q",
				path,
			) {
				t.Fatal("decision conflict left the deployment page")
			}
		}
		for _, theme := range []string{"light", "mocha"} {
			browser.evaluate(
				t,
				fmt.Sprintf(
					`document.documentElement.setAttribute('data-theme', %q)`,
					theme,
				),
			)
			for _, width := range []int{375, 768, 1280} {
				browser.call(
					t,
					"Emulation.setDeviceMetricsOverride",
					map[string]any{
						"width": width, "height": 900, "deviceScaleFactor": 1,
						"mobile": false,
					},
					&struct{}{},
				)
				if string(
					browser.evaluate(
						t,
						`document.documentElement.scrollWidth <= innerWidth`,
					),
				) != "true" {
					t.Fatal("artifact decision state overflows viewport")
				}
				browser.screenshot(
					t,
					fmt.Sprintf("artifact-%s-%s-%d", state, theme, width),
				)
			}
		}
	}
	// The fresh form can still approve and execute the immutable saved plan.
	browser.evaluate(
		t,
		`document.querySelector('form[action$="/approve"] button').click()`,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	browser.wait(
		t,
		`document.querySelector('#status-badge')?.innerText.includes('succeeded') && !document.querySelector('form[action$="/approve"]')`,
	)
}

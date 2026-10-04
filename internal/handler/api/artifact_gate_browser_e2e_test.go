//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/events"
)

func TestArtifactGateBrowserE2E(t *testing.T) {
	// Given: an API-generated artifact waiting for administrator review.
	f, deployment, _ := newGateDeployment(t)
	f.api(t, "PUT", f.base(), map[string]string{"name": "Artifact review"}, 200)
	browser := startPackageBrowser(t)
	var cookie struct {
		Success bool `json:"success"`
	}
	browser.call(
		t,
		"Network.setCookie",
		map[string]any{
			"name":     "session",
			"value":    f.session,
			"url":      f.baseURL,
			"httpOnly": true,
		},
		&cookie,
	)
	if !cookie.Success {
		t.Fatal("session cookie rejected")
	}
	page := fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID)
	for _, width := range []int{375, 768, 1280} {
		browser.call(
			t,
			"Emulation.setDeviceMetricsOverride",
			map[string]any{
				"width":             width,
				"height":            900,
				"deviceScaleFactor": 1,
				"mobile":            false,
			},
			&struct{}{},
		)
		browser.call(
			t,
			"Page.navigate",
			map[string]string{"url": page},
			&struct{}{},
		)
		browser.wait(
			t,
			`document.querySelector('section[aria-label="Artifact approval"]') && document.querySelector('form[action$="/approve"]')`,
		)
		browser.wait(
			t,
			`document.querySelector('section[aria-label="Artifact approval"]').innerText.includes('Unverified summary supplied by the deployment step.')`,
		)
		if string(
			browser.evaluate(
				t,
				`document.documentElement.scrollWidth <= innerWidth`,
			),
		) != "true" {
			t.Fatal("horizontal overflow")
		}
		if string(
			browser.evaluate(
				t,
				`document.body.innerText.includes('generated-sensitive-value') || document.body.innerText.includes('exact-approved-plan-secret')`,
			),
		) != "false" {
			t.Fatal("sensitive content leaked")
		}
		browser.screenshot(t, fmt.Sprintf("artifact-review-%d", width))
	}
	// Polling must preserve the keyboard user's focused decision control.
	if string(
		browser.evaluate(
			t,
			`new Promise(resolve => { const button = document.querySelector('form[action$="/approve"] button'); button.focus(); setTimeout(() => resolve(document.activeElement === button), 3500); })`,
		),
	) != "true" {
		t.Fatal("approval polling discarded keyboard focus")
	}
	browser.screenshot(t, "artifact-focused-approval")
	// When: the actual web form approves this checksum and revision.
	browser.evaluate(
		t,
		`document.querySelector('form[action$="/approve"] button').click()`,
	)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	// Then: the review becomes approved and apply completes.
	browser.wait(
		t,
		`document.querySelector('section[aria-label="Artifact approval"]')?.innerText.includes('approved')`,
	)
	browser.wait(
		t,
		`document.querySelector('#status-badge')?.innerText.includes('succeeded')`,
	)
	browser.wait(t, `!document.querySelector('[hx-post$="/cancel"]')`)
	browser.screenshot(t, "artifact-approved")
	browser.wait(
		t,
		`!document.querySelector('[data-artifact-gates]').hasAttribute('hx-trigger')`,
	)
	// HTMX cancellation must swap only the badge, then stop gate polling.
	release, err := f.h.repo.Queries.GetDeploymentRelease(
		t.Context(), deployment.ReleaseID,
	)
	if err != nil {
		t.Fatal(err)
	}
	paused := verificationDeploy(t, f, release)
	f.completion(t, paused.ID, events.ArtifactAwaitingApproval)
	browser.call(t, "Page.navigate", map[string]string{
		"url": fmt.Sprintf("%s/deployments/%d", f.baseURL, paused.ID),
	}, &struct{}{})
	browser.wait(t, `document.querySelector('form[action$="/approve"]')`)
	browser.evaluate(
		t,
		`document.querySelector('[hx-post$="/cancel"]').click()`,
	)
	browser.wait(
		t,
		`document.querySelector('#status-badge')?.innerText.includes('cancelled')`,
	)
	browser.wait(t, `!document.querySelector('[hx-post$="/cancel"]')`)
	browser.wait(
		t,
		`!document.querySelector('[data-artifact-gates]').hasAttribute('hx-trigger')`,
	)
	if string(
		browser.evaluate(t, `document.querySelectorAll('h1').length`),
	) != "1" {
		t.Fatal("cancellation swapped a full page into the status badge")
	}
	browser.screenshot(t, "artifact-cancelled")
	// And: configuration fields render at mobile and desktop widths.
	for _, width := range []int{375, 1280} {
		browser.call(
			t,
			"Emulation.setDeviceMetricsOverride",
			map[string]any{
				"width":             width,
				"height":            900,
				"deviceScaleFactor": 1,
				"mobile":            false,
			},
			&struct{}{},
		)
		for _, surface := range []struct{ name, path, field string }{
			{"step", fmt.Sprintf("/projects/%d/steps-page", f.project.ID), "network_mode"},
			{"template", "/templates/new", "network_mode"},
			{"runbook", fmt.Sprintf("/projects/%d/runbooks/new", f.project.ID), "step_network"},
		} {
			browser.call(
				t,
				"Page.navigate",
				map[string]string{"url": f.baseURL + surface.path},
				&struct{}{},
			)
			browser.wait(
				t,
				`document.readyState === 'complete' && window.htmx !== undefined`,
			)
			if surface.name == "step" {
				browser.evaluate(
					t,
					`document.querySelector('[hx-get$="/steps/new"]').click()`,
				)
			}
			browser.wait(
				t,
				fmt.Sprintf(
					`document.querySelector('[name="%s"]')`,
					surface.field,
				),
			)
			browser.screenshot(
				t,
				fmt.Sprintf("artifact-%s-form-%d", surface.name, width),
			)
			if string(
				browser.evaluate(
					t,
					`document.documentElement.scrollWidth <= innerWidth`,
				),
			) != "true" {
				t.Fatalf(
					"%s configuration horizontal overflow: %s",
					surface.name,
					browser.evaluate(
						t,
						`Array.from(document.querySelectorAll('body *')).filter(e=>e.getBoundingClientRect().right>innerWidth+1).map(e=>({tag:e.tagName,class:e.className,width:e.clientWidth})).slice(-12)`,
					),
				)
			}
		}
	}
	var gates json.RawMessage
	if err := json.Unmarshal(
		f.api(t, "GET", gateAPIPath(deployment.ID), nil, 200),
		&gates,
	); err != nil {
		t.Fatal(err)
	}
}

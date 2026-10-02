//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestPackageRepositoryBrowserE2E(t *testing.T) {
	// Given: a real browser, authenticated session, and private HTTPS ZIP source.
	f := newArtifactE2E(t)
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
		t.Fatal("test browser session was not established")
	}
	formURL := fmt.Sprintf(
		"%s/projects/%d/package-repository/edit",
		f.baseURL,
		f.project.ID,
	)
	browser.call(
		t,
		"Page.navigate",
		map[string]string{"url": formURL},
		&struct{}{},
	)
	browser.wait(
		t,
		"document.readyState === 'complete' && window.Alpine && document.querySelector('[name=auth_type]') && !document.querySelector('[x-cloak]')",
	)

	// When: switching authentication modes at each supported viewport.
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
		for _, auth := range []string{"noauth", "bearer", "basic"} {
			browser.evaluate(
				t,
				fmt.Sprintf(
					`(() => { const select = document.querySelector('[name=auth_type]'); select.value = %q; select.dispatchEvent(new Event('change', {bubbles:true})); return true; })()`,
					auth,
				),
			)
			browser.wait(
				t,
				fmt.Sprintf(
					`document.querySelector('[name=username]').disabled === %t && document.querySelector('[name=credential]').disabled === %t && (document.querySelector('[name=username]').getClientRects().length > 0) === %t && (document.querySelector('[name=credential]').getClientRects().length > 0) === %t`,
					auth != "basic",
					auth == "noauth",
					auth == "basic",
					auth != "noauth",
				),
			)
			var state struct{ Username, Credential, Required, Overflow bool }
			if err := json.Unmarshal(
				browser.evaluate(
					t,
					`(() => ({ Username: document.querySelector('[name=username]').getClientRects().length > 0, Credential: document.querySelector('[name=credential]').getClientRects().length > 0, Required: document.querySelector('[name=username]').required, Overflow: document.documentElement.scrollWidth > innerWidth }))()`,
				),
				&state,
			); err != nil {
				t.Fatal(err)
			}
			// Then: only relevant fields are visible/enabled, without horizontal overflow.
			if state.Username != (auth == "basic") ||
				state.Credential != (auth != "noauth") ||
				state.Required != (auth == "basic") ||
				state.Overflow {
				t.Fatalf("auth=%s width=%d field state=%+v", auth, width, state)
			}
			browser.screenshot(t, fmt.Sprintf("form-%d-%s", width, auth))
		}
	}
	// When: the user saves a bearer source without a separate selection action.
	browser.evaluate(
		t,
		fmt.Sprintf(
			`(() => { const auth = document.querySelector('[name=auth_type]'); auth.value = 'bearer'; auth.dispatchEvent(new Event('change', {bubbles:true})); document.querySelector('[name=url_template]').value = %q; document.querySelector('[name=credential]').value = 'artifact-secret'; return true; })()`,
			f.upstream+"/{version}.zip",
		),
	)
	browser.wait(t, "!document.querySelector('[name=credential]').disabled")
	browser.wire.events = nil
	browser.evaluate(
		t,
		"document.querySelector('form[action$=\"/package-repository\"]').requestSubmit(); true",
	)
	if err := browser.wire.waitEvent(
		"Page.frameStoppedLoading",
		browser.session,
	); err != nil {
		t.Fatal(err)
	}
	browser.wait(
		t,
		"document.querySelector('form[hx-post$=\"/package-repository/test\"]') !== null",
	)
	// Then: the test control is immediately available and its HTMX response swaps in.
	browser.evaluate(
		t,
		"document.querySelector('[name=version]').value = '1.6.0'; document.querySelector('form[hx-post$=\"/package-repository/test\"]').requestSubmit(); true",
	)
	browser.wait(
		t,
		"document.querySelector('#package-test-result [data-package-test=success]') !== null",
	)
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
		if string(
			browser.evaluate(
				t,
				"document.documentElement.scrollWidth > innerWidth",
			),
		) != "false" {
			t.Fatal("package test result overflows viewport")
		}
		browser.screenshot(t, fmt.Sprintf("result-%d-success", width))
	}
	f.changePackage("missing")
	browser.evaluate(
		t,
		"document.querySelector('form[hx-post$=\"/package-repository/test\"]').requestSubmit(); true",
	)
	browser.wait(
		t,
		"document.querySelector('#package-test-result [role=alert]') !== null",
	)
	browser.screenshot(t, "result-error")
}

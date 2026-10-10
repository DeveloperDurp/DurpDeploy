//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestReleaseDeferredPackageBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "PUT", f.base()+"/package-repository", map[string]any{
		"url_template": f.upstream + "/{version}.zip",
		"auth_type":    "bearer", "credential": "artifact-secret",
	}, 200)
	f.changePackage("missing")
	browser := startPackageBrowser(t)
	var cookie struct{ Success bool }
	browser.call(t, "Network.setCookie", map[string]any{
		"name": "session", "value": f.session,
		"url": f.baseURL, "httpOnly": true,
	}, &cookie)
	if !cookie.Success {
		t.Fatal("browser session was not established")
	}
	pageURL := fmt.Sprintf("%s/projects/%d/releases", f.baseURL, f.project.ID)
	browser.call(t, "Page.navigate", map[string]string{"url": pageURL},
		&struct{}{})
	browser.wait(
		t,
		"document.readyState === 'complete' && window.htmx && document.querySelector('[name=version]')",
	)
	browser.evaluate(
		t,
		`document.querySelector('[name=version]').value = '2.0.0'; document.querySelector('form[hx-post$="/releases"] button.btn-primary').click(); true`,
	)
	browser.wait(
		t,
		"document.querySelector('#releases-content tbody a') !== null",
	)
	captureReleaseState(t, browser, "release-deferred-list")
	browser.evaluate(
		t,
		"document.querySelector('#releases-content tbody a').click(); true",
	)
	browser.wait(
		t,
		"document.querySelector('[data-package-pending][role=status]') !== null",
	)
	captureReleaseState(t, browser, "release-deferred-detail")
	f.changePackage("package")
	browser.evaluate(
		t,
		"window.confirm = () => true; document.querySelector('form[action$=\"/refresh\"] button').click(); true",
	)
	browser.wait(
		t,
		"document.querySelector('[data-package-pending]') === null && document.body.textContent.includes('Pinned ZIP package')",
	)
	captureReleaseState(t, browser, "release-pinned-detail")
}

func captureReleaseState(t *testing.T, browser *packageBrowser, state string) {
	t.Helper()
	for _, width := range []int{375, 768, 1280} {
		browser.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		if string(
			browser.evaluate(
				t,
				"document.documentElement.scrollWidth > innerWidth",
			),
		) != "false" {
			t.Fatalf("%s overflows viewport %d", state, width)
		}
		browser.screenshot(t, fmt.Sprintf("%s-%d", state, width))
	}
}

//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestLoginDestinationBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	b.call(t, "Page.navigate", map[string]string{
		"url": f.baseURL + path + "?source=login-context",
	}, &struct{}{})
	b.wait(
		t,
		`location.pathname==='/login' && document.querySelector('#login-email')`,
	)
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 375, "height": 900, "deviceScaleFactor": 1, "mobile": true,
	}, &struct{}{})
	b.evaluate(t, `(() => {
 const form=document.querySelector('form[action="/login"]');
 form.elements.email.value='artifact-e2e@example.com';
 form.elements.password.value='wrong-test-password';
 form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		`document.querySelector('[role=alert]')?.textContent.includes('Invalid email or password')
 && document.querySelector('#login-email').value==='artifact-e2e@example.com'
 && document.querySelector('#login-password').value===''
 && document.readyState==='complete'`,
	)
	b.screenshot(t, "login-context-error-phone")
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 1280, "height": 900, "deviceScaleFactor": 1, "mobile": false,
	}, &struct{}{})
	b.screenshot(t, "login-context-error-desktop")
	b.evaluate(t, `(() => {
 const form=document.querySelector('form[action="/login"]');
 form.elements.password.value='testpass'; form.requestSubmit(); return true;
})()`)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname===%q && location.search==='?source=login-context'
 && document.readyState==='complete' && document.querySelector('#app-navbar')
 && !document.querySelector('#login-password')`,
			path,
		),
	)
	b.captureNavigation(t, "login-context-returned-project")
	b.navigateBackTest(t, f.baseURL+"/login?next=%2F%2Fevil.test")
	b.wait(
		t,
		`document.querySelector('#login-password') && document.readyState==='complete'`,
	)
	b.evaluate(t, `(() => {
 const form=document.querySelector('form[action="/login"]');
 form.elements.email.value='artifact-e2e@example.com';
 form.elements.password.value='testpass'; form.requestSubmit(); return true;
})()`)
	b.wait(t, `location.pathname==='/' && document.readyState==='complete'
 && !document.querySelector('#login-password')`)
}

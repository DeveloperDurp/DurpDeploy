//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestSelfPasswordChangeBrowserE2E(t *testing.T) {
	f := newPermissionFixture(t)
	b := startPackageBrowser(t)
	for _, width := range []int{390, 768, 1280} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			user := seedAPIUser(
				t,
				f.h.repo,
				fmt.Sprintf("browser-%d@example.test", width),
				"viewer",
			)
			session := passwordFreshSession(t, f, user.ID)
			b.setBackTestSession(t, f.server.URL, session)
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width":             width,
				"height":            520,
				"deviceScaleFactor": 1,
				"mobile":            false,
			}, &struct{}{})
			b.navigateBackTest(t, f.server.URL+"/settings/security")
			if string(b.evaluate(t, `(() => {
 const password = document.querySelector('#change-password details');
 const signin = document.querySelector('[aria-labelledby="security-signin-title"]').getBoundingClientRect();
 const mfa = document.querySelector('[aria-labelledby="security-mfa-title"]').getBoundingClientRect();
 const status = document.querySelector('[aria-label="Security status"]');
 return !password.open && status.querySelector('svg') &&
  status.textContent.includes('MFA is not enabled') &&
  status.querySelector('a').textContent.trim() === 'Confirm identity' &&
  !document.querySelector('form[action="/settings/security/recovery/regenerate"]') && (innerWidth >= 640
  ? mfa.left >= signin.right && Math.abs(mfa.top - signin.top) < 2
  : mfa.top >= signin.bottom);
})()`)) != "true" {
				t.Fatal("security groups or collapsed password layout failed")
			}
			b.screenshot(t, fmt.Sprintf("security-groups-%d", width))
			b.evaluate(
				t,
				`document.querySelector('#change-password summary').click(); true`,
			)
			if string(b.evaluate(t, `(() => {
 const fields = ['current-password', 'new-password', 'confirm-password'];
 return fields.every(id => {
  const field = document.getElementById(id);
  const label = document.querySelector('label[for="'+id+'"]');
  return field?.type === 'password' && field.required && label?.textContent.trim();
 }) && document.documentElement.scrollWidth <= innerWidth;
})()`)) != "true" {
				t.Fatal("password form labels or mobile width failed")
			}
			b.evaluate(
				t,
				`document.querySelector('label[for="current-password"]').click(); true`,
			)
			b.wait(t, `document.activeElement.id === 'current-password'`)
			b.call(
				t,
				"Input.dispatchKeyEvent",
				map[string]any{
					"type":                  "keyDown",
					"key":                   "Tab",
					"code":                  "Tab",
					"windowsVirtualKeyCode": 9,
				},
				&struct{}{},
			)
			b.call(
				t,
				"Input.dispatchKeyEvent",
				map[string]any{
					"type":                  "keyUp",
					"key":                   "Tab",
					"code":                  "Tab",
					"windowsVirtualKeyCode": 9,
				},
				&struct{}{},
			)
			b.wait(t, `document.activeElement.id === 'new-password'`)
			// A short viewport exercises scrolling while a phone keyboard is open.
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width":             width,
				"height":            280,
				"deviceScaleFactor": 1,
				"mobile":            false,
			}, &struct{}{})
			b.evaluate(
				t,
				`document.querySelector('#confirm-password').focus(); true`,
			)
			b.wait(
				t,
				`(() => {const r = document.activeElement.getBoundingClientRect();
 return document.activeElement.id === 'confirm-password' && r.top >= 0 && r.bottom <= innerHeight;})()`,
			)
			passwordBrowserSubmit(t, b, "mismatch-password")
			b.wait(
				t,
				`document.querySelector('#change-password details').open && document.querySelector('#password-error') &&
 [...document.querySelectorAll('#change-password input[type=password]')].every(input => input.value === '')`,
			)
			b.wait(t, `document.activeElement.id === 'password-error'`)
			b.screenshot(t, fmt.Sprintf("password-inline-error-%d", width))
			b.evaluate(t,
				`document.querySelector('#change-password a').click(); true`,
			)
			b.wait(
				t,
				`!document.querySelector('#change-password details').open &&
 !document.querySelector('#password-error') &&
 [...document.querySelectorAll('#change-password input[type=password]')].every(input => input.value === '')`,
			)
			b.evaluate(
				t,
				`document.querySelector('#change-password summary').click(); true`,
			)
			passwordBrowserSubmit(t, b, "New-password-141")
			b.wait(t, `location.pathname === '/login'`)
			b.evaluate(t, fmt.Sprintf(`(() => {
 const form=document.querySelector('form[action="/login"]');
 form.elements.email.value=%q; form.elements.password.value='testpass';
 form.requestSubmit(); return true;
})()`, user.Email))
			b.wait(
				t,
				`location.pathname === '/login' && document.body.innerText.includes('Invalid')`,
			)
			b.evaluate(t, `(() => {
 const form=document.querySelector('form[action="/login"]');
 form.elements.password.value='New-password-141';
 form.requestSubmit(); return true;
})()`)
			b.wait(t, `location.pathname === '/'`)
			b.screenshot(t, fmt.Sprintf("password-new-login-%d", width))
		})
	}
}

func passwordBrowserSubmit(
	t *testing.T,
	b *packageBrowser,
	confirmation string,
) {
	t.Helper()
	b.evaluate(t, fmt.Sprintf(`(() => {
 const form=document.querySelector('#change-password form');
 form.elements.current_password.value='testpass';
 form.elements.new_password.value='New-password-141';
 form.elements.password_confirmation.value=%q;
 form.requestSubmit(); return true;
})()`, confirmation))
}

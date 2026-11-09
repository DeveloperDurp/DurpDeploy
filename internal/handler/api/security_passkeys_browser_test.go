//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestPasskeyWarningBrowserE2E(t *testing.T) {
	f := newPermissionFixture(t)
	owner := seedAPIUser(t, f.h.repo, "owner@warning-browser.test", "viewer")
	seedWarningPasskey(t, f, owner.ID)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL,
		memberRoleSession(t, f.h, owner.ID))
	for _, width := range []int{390, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1,
			"mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.server.URL+"/settings/security")
		b.wait(t, `(() => {
 const warning = document.querySelector('[data-passkey-counter-warning]');
 return warning?.textContent.includes('Counter warning') &&
 warning.textContent.includes('does not prove cloning') &&
 warning.parentElement.querySelector('input[name=name]').value === 'Counter review key' &&
 document.documentElement.scrollWidth <= innerWidth;
})()`)
		b.screenshot(t, fmt.Sprintf("passkey-counter-warning-%d", width))
	}
	other := seedAPIUser(t, f.h.repo, "other@warning-browser.test", "admin")
	b.setBackTestSession(t, f.server.URL,
		memberRoleSession(t, f.h, other.ID))
	b.navigateBackTest(t, f.server.URL+"/settings/security")
	if string(b.evaluate(
		t,
		`!!document.querySelector('[data-passkey-counter-warning]')`,
	)) != "false" {
		t.Fatal("another account sees the owner's warning")
	}
}

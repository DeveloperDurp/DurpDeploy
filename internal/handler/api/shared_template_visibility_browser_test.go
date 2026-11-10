//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestSharedTemplateVisibilityBrowserE2E(t *testing.T) {
	f := newSharedTemplateFixture(t)
	reader := seedAPIUser(t, f.h.repo, "shared-browser@example.test", "viewer")
	b := startPackageBrowser(t)
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 1440, "height": 900, "deviceScaleFactor": 1, "mobile": false,
	}, &struct{}{})
	b.setBackTestSession(t, f.server.URL, memberRoleSession(t, f.h, reader.ID))
	b.navigateBackTest(t, f.server.URL+"/templates")
	b.wait(
		t,
		`document.querySelector('#templates-list')?.textContent.includes('shared-current-literal')`,
	)
	b.screenshot(t, "shared-template-viewer-list")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/templates/%d/history", f.server.URL, f.template.ID),
	)
	b.evaluate(
		t,
		`document.querySelectorAll('details[data-disclosure="template-history-script"]').forEach(el => el.open = true); true`,
	)
	b.wait(
		t,
		`document.querySelector('details[open] code')?.getClientRects().length > 0 && document.querySelector('#template-history').textContent.includes('shared-original-literal')`,
	)
	b.screenshot(t, "shared-template-viewer-history")
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 390, "height": 844, "deviceScaleFactor": 1, "mobile": true,
	}, &struct{}{})
	b.evaluate(
		t,
		`document.querySelectorAll('details[data-disclosure="template-history-script-mobile"]').forEach(el => el.open = true); true`,
	)
	b.wait(
		t,
		`document.documentElement.scrollWidth <= innerWidth && document.querySelector('[data-mobile-template-history-list] details[open] code')?.getClientRects().length > 0`,
	)
	b.screenshot(t, "shared-template-viewer-history-phone")
}

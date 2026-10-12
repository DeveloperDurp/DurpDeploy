//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestSMTPPlaintextWarningBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	publishPlaintextNotification(t, f)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, width := range []int{375, 768, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1,
			"mobile": width < 768,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+"/admin/notifications")
		b.wait(
			t,
			`Array.from(document.querySelectorAll('.badge-warning')).some(e => e.getBoundingClientRect().width > 0 && e.textContent.includes('email: ok: sent without TLS'))`,
		)
		if string(b.evaluate(t,
			`document.documentElement.scrollWidth <= innerWidth`)) != "true" {
			t.Fatalf("notification history overflows at %dpx", width)
		}
		b.screenshot(t, fmt.Sprintf("smtp-warning-%d", width))
		b.evaluate(
			t,
			`Array.from(document.querySelectorAll('[data-modal-target]')).find(e => e.getBoundingClientRect().width > 0).click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('dialog[open] .badge-warning')?.textContent.includes('email: ok: sent without TLS')`,
		)
		b.screenshot(t, fmt.Sprintf("smtp-warning-modal-%d", width))
		b.evaluate(t, `document.querySelector('dialog[open]').close(); true`)
	}
}

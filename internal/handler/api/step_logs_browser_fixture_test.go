//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func (b *packageBrowser) setStepLogSession(
	t *testing.T,
	address, session string,
) {
	t.Helper()
	var result struct {
		Success bool `json:"success"`
	}
	b.call(
		t,
		"Network.setCookie",
		map[string]any{
			"name":     "session",
			"value":    session,
			"url":      address,
			"httpOnly": true,
		},
		&result,
	)
	if !result.Success {
		t.Fatal("browser session missing")
	}
}

func (b *packageBrowser) openStepLogPage(t *testing.T, address string) {
	t.Helper()
	b.wire.events = nil
	b.call(t, "Page.navigate", map[string]string{"url": address}, &struct{}{})
	if err := b.wire.waitEvent("Page.frameStoppedLoading", b.session); err != nil {
		t.Fatal(err)
	}
	b.wait(
		t,
		fmt.Sprintf(
			`location.href === %q && document.readyState === 'complete' && window.Alpine && document.querySelector('[x-data^="deploymentStepLogs"]')`,
			address,
		),
	)
}

func (b *packageBrowser) captureStepLogs(t *testing.T, name string) {
	t.Helper()
	for _, width := range []int{375, 768, 1280} {
		b.call(
			t,
			"Emulation.setDeviceMetricsOverride",
			map[string]any{
				"width":             width,
				"height":            1100,
				"deviceScaleFactor": 1,
				"mobile":            false,
			},
			&struct{}{},
		)
		if string(
			b.evaluate(t, `document.documentElement.scrollWidth <= innerWidth`),
		) != "true" {
			t.Fatal("step panels overflow viewport")
		}
		b.screenshot(t, fmt.Sprintf("step-logs-%s-%d", name, width))
	}
}

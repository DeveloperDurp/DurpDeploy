//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/config"
)

func TestAPITokenTelemetryBrowserE2E(t *testing.T) {
	settings := config.Defaults()
	settings.Limits.TokenTelemetry = config.TokenTelemetryLimits{
		Concurrency: 2, IntervalSeconds: 120, WriteTimeoutSeconds: 1,
	}
	f := newConfiguredTokenUsageFixture(t, false, settings)
	close(f.probe.release)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.srv.URL, f.session)
	b.navigateBackTest(t, f.srv.URL+"/settings/tokens")
	f.get(t, f.token, "/api/v1/users/me", 200)
	var stamp string
	waitTokenUsage(t, func() bool {
		row, err := f.repo.Queries.GetApiTokenByID(t.Context(), f.tokenID)
		if err == nil && row.LastUsedAt.Valid {
			stamp = time.Unix(row.LastUsedAt.Int64, 0).
				Format("2006-01-02 15:04:05")
			return true
		}
		return false
	})
	for _, width := range []int{375, 1280} {
		b.call(
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
		for _, path := range []string{"/settings/tokens", "/admin/tokens"} {
			if width == 375 && path == "/admin/tokens" {
				continue
			}
			b.navigateBackTest(t, f.srv.URL+path)
			b.wait(
				t,
				fmt.Sprintf(`document.body.textContent.includes(%q)`, stamp),
			)
			if path == "/settings/tokens" &&
				string(b.evaluate(t, fmt.Sprintf(`(() => {
 const record=[...document.querySelectorAll('[data-token-record=%q]')].find(el=>el.getClientRects().length);
 return !!record && record.textContent.includes(%q) && document.documentElement.scrollWidth<=innerWidth;
})()`, f.tokenID, stamp))) != "true" {
				t.Fatal("visible token record lost last-used reporting")
			}
			b.screenshot(
				t,
				fmt.Sprintf(
					"token-usage-%d-admin-%t",
					width,
					path == "/admin/tokens",
				),
			)
		}
	}
}

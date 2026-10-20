//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReleaseDuplicateRecoveryBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	version := `edge-<b>&"v1`
	release := verificationRelease(t, f, version)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := strings.TrimPrefix(f.base(), "/api/v1") + "/releases"
	for index, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 844, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+path)
		b.evaluate(t, fmt.Sprintf(`(() => {
 const input = document.querySelector('#release-version');
 input.value = %q;
 input.form.requestSubmit();
 return true;
})()`, version))
		b.wait(
			t,
			`document.querySelector('#release-create-error') && !document.querySelector('.htmx-request,.htmx-settling')`,
		)
		if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const input = document.querySelector('#release-version');
 const error = document.querySelector('#release-create-error');
 const toast = document.querySelector('[x-data="toast"]');
 return input.value === %q && error.textContent.includes('different version') &&
 input.getAttribute('aria-describedby') === error.id && error.getAttribute('role') === 'alert' &&
 !document.querySelector('#releases-content b') && location.pathname === %q &&
 Alpine.$data(toast).type === 'error' && document.documentElement.scrollWidth <= innerWidth;
})()`, version, path))) != "true" {
			b.screenshot(t, "release-duplicate-recovery-failure")
			t.Fatal(
				"duplicate rejection lost input, safe recovery, or error feedback",
			)
		}
		checkDuplicateReleaseCount(t, f, index+1)
		b.screenshot(t, fmt.Sprintf("release-duplicate-recovery-%d", width))
		corrected := fmt.Sprintf("recovered-%d", width)
		b.evaluate(t, fmt.Sprintf(`(() => {
 const input = document.querySelector('#release-version');
 input.value = %q;
 input.form.requestSubmit();
 return true;
})()`, corrected))
		b.wait(
			t,
			fmt.Sprintf(
				`!document.querySelector('.htmx-request,.htmx-settling') && !document.querySelector('#release-create-error') && [...document.querySelectorAll('#releases-content tbody tr')].some(r => r.textContent.includes(%q))`,
				corrected,
			),
		)
		checkDuplicateReleaseCount(t, f, index+2)
	}
	// Recovery creates new releases without replacing the original snapshot.
	f.api(
		t,
		"GET",
		fmt.Sprintf("%s/releases/%d", f.base(), release.ID),
		nil,
		200,
	)
}

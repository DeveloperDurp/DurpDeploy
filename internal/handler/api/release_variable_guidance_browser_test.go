//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestReleaseVariableGuidanceBrowserE2E(t *testing.T) {
	fixture := newReleaseVariableGuidance(t)
	fixture.change(t)
	f := fixture.f
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := strings.TrimPrefix(fixture.path(), "/api/v1")
	for _, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 844, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+path)
		checkReleaseVariableGuidance(t, b, "old")
		b.screenshot(t, fmt.Sprintf("release-variable-guidance-%d", width))
	}
	b.wire.events = nil
	b.evaluate(
		t,
		`setTimeout(() => document.querySelector('form[action$="/refresh"] button').click(), 0); true`,
	)
	if err := b.wire.waitEvent("Page.javascriptDialogOpening", b.session); err != nil {
		t.Fatal(err)
	}
	b.call(
		t,
		"Page.handleJavaScriptDialog",
		map[string]bool{"accept": true},
		&struct{}{},
	)
	b.wait(
		t,
		`!document.querySelector('.htmx-request,.htmx-settling') && [...document.querySelectorAll('tr')].some(r => r.cells[0]?.textContent.trim() === 'VALUE' && r.cells[1]?.textContent.trim() === 'new')`,
	)
	checkReleaseVariableGuidance(t, b, "new")
	fixture.checkSnapshot(t, "new")
	b.screenshot(t, "release-variable-guidance-refreshed")
}

func checkReleaseVariableGuidance(
	t *testing.T,
	b *packageBrowser,
	want string,
) {
	t.Helper()
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const project = document.querySelector('[data-release-project-guidance]');
 const lifecycle = document.querySelector('[data-release-lifecycle-guidance]');
 if (!project || !lifecycle || project === lifecycle) return false;
 project.scrollIntoView({block:'start'});
 const card = project.closest('.card');
 const row = [...card.querySelectorAll('tbody tr')].find(r => r.cells[0]?.textContent.trim() === 'VALUE');
 return project.tagName === 'P' && lifecycle.tagName === 'P' &&
 project.querySelector('strong')?.textContent === 'Project variables:' &&
 lifecycle.querySelector('strong')?.textContent === 'Lifecycle variables:' &&
 project.textContent.includes('Refresh') && lifecycle.textContent.includes('No release refresh is required') &&
 lifecycle.textContent.includes('current values and scope') &&
 row?.cells[1]?.textContent.trim() === %q && card.textContent.includes('••••••••') &&
 !card.textContent.includes('guidance-secret-sentinel') &&
 project.getBoundingClientRect().height > 0 && lifecycle.getBoundingClientRect().height > 0;
})()`, want))) != "true" {
		b.screenshot(t, "release-variable-guidance-failure")
		t.Fatal(
			"source-specific guidance or visible snapshot values are missing",
		)
	}
}

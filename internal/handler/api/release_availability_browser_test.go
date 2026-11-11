//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestReleaseAvailabilityBrowserE2E(t *testing.T) {
	f := newReleaseAvailabilityFixture(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1,
			"mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+"/projects")
		b.wait(t, `document.querySelector('[data-release-update]')`)
		if string(
			b.evaluate(
				t,
				`document.querySelector('[data-release-update]').textContent === 'New release' &&
 !document.querySelector('[data-environment-release-update]')`,
			),
		) != "true" {
			t.Fatal("project cards repeat release details")
		}
		b.evaluate(t, `(() => {
 const form = document.querySelector('#project-filters');
 form.querySelector('[name=updates_available]').click();
 form.requestSubmit(); return true;
})()`)
		b.wait(
			t,
			`location.search.includes('updates_available=true') && document.querySelector('[name=updates_available]').checked`,
		)
		b.screenshot(t, fmt.Sprintf("release-availability-filter-%d", width))
		b.navigateBackTest(
			t,
			fmt.Sprintf("%s/projects/%d", f.baseURL, f.project.ID),
		)
		b.wait(
			t,
			`document.querySelectorAll('[data-environment-release-update]').length === 2`,
		)
		if string(
			b.evaluate(
				t,
				`document.body.textContent.includes('Ready to deploy') &&
 document.body.textContent.includes('Waiting for preceding stage') &&
 document.documentElement.scrollWidth <= innerWidth`,
			),
		) != "true" {
			t.Fatal("release status missing or overflows viewport")
		}
		b.screenshot(t, fmt.Sprintf("release-availability-%d", width))
	}
	seedDeployment(t, f.h.repo, f.latest.ID, f.environment.ID, "succeeded")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d", f.baseURL, f.project.ID),
	)
	b.wait(
		t,
		`document.querySelectorAll('[data-environment-release-update]').length === 1 && document.body.textContent.includes('Approval required')`,
	)
	seedDeployment(t, f.h.repo, f.latest.ID, f.nextEnv, "succeeded")
	b.navigateBackTest(t, f.baseURL+"/projects?updates_available=true")
	b.wait(
		t,
		`document.body.textContent.includes('No matching projects.') && !document.querySelector('[data-release-update]')`,
	)
	b.screenshot(t, "release-availability-current")
}

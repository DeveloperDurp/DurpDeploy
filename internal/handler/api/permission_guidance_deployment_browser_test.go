//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestPermissionGuidanceDeploymentBrowserE2E(t *testing.T) {
	f := newPermissionGuidanceFixture(t)
	id := f.history(t)
	b := startPackageBrowser(t)
	for _, pair := range [][2]string{
		{"deployer", "viewer"}, {"viewer", "deployer"},
		{"maintainer", "viewer"}, {"maintainer", "deployer"},
	} {
		actor := f.actor(t, pair[0], pair[1])
		b.setBackTestSession(t, f.server.URL, actor.session)
		for _, width := range []int{390, 1280} {
			b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
				"width": width, "height": 900, "deviceScaleFactor": 1,
				"mobile": false,
			}, &struct{}{})
			path := fmt.Sprintf("/projects/%d/deploy", f.project.ID)
			globalHelp := pair[0] == "viewer"
			if pair[0] == "maintainer" {
				path = fmt.Sprintf("/deployments/%d/rollback", id)
			}
			b.navigateBackTest(t, f.server.URL+path)
			b.wait(t, fmt.Sprintf(`(() => {
 const page=document.querySelector('#page-content');
 const links=[...page.querySelectorAll('a')].map(a=>a.getAttribute('href'));
 return links.includes('/admin/users') === %t &&
 links.includes(%q) === %t && !page.querySelector('form') &&
 document.documentElement.scrollWidth <= innerWidth;
})()`, globalHelp, fmt.Sprintf("/projects/%d/edit#project-members", f.project.ID), !globalHelp))
			b.screenshot(
				t,
				fmt.Sprintf(
					"permission-action-recovery-%s-%s-%d",
					pair[0],
					pair[1],
					width,
				),
			)
		}
	}
}

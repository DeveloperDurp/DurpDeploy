//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestProjectPanelFailureBrowserE2E(t *testing.T) {
	f, fault := newProjectPanelFailureFixture(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	for _, query := range []string{
		"ListProjectPanelDeployments",
		"ListEnvironments",
	} {
		fault.query.Store(query)
		for _, page := range []string{"/projects", path} {
			b.call(t, "Page.navigate", map[string]string{
				"url": f.baseURL + page,
			}, &struct{}{})
			b.wait(t, fmt.Sprintf(
				`location.pathname === %q && document.readyState === 'complete' && document.body.textContent.trim() === 'Internal server error'`,
				page,
			))
			if string(b.evaluate(
				t,
				`document.querySelector('#projects-list,#project-detail') === null && !/No deployments yet|No successful deploys/.test(document.body.textContent)`,
			)) != "true" {
				t.Fatal("a database failure rendered a misleading empty panel")
			}
		}
		fault.query.Store("")
		b.navigateBackTest(t, f.baseURL+path)
		b.wait(
			t,
			`document.querySelector('#project-detail') && document.body.textContent.includes('No successful deploys')`,
		)
	}
	b.screenshot(t, "project-panel-empty-after-database-recovery")
}

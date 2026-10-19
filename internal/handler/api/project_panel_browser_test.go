//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestProjectPanelScopeBrowserE2E(t *testing.T) {
	p := newProjectPanelFixture(t)
	f := p.f
	b := startPackageBrowser(t)
	for _, lifecycleID := range []int64{0, p.lifecycleID} {
		f.api(t, "PUT", f.base(), map[string]any{
			"name": f.project.Name, "lifecycle_id": lifecycleID,
		}, 200)
		for _, role := range []string{"viewer", "deployer"} {
			user := seedAPIUser(
				t,
				f.h.repo,
				fmt.Sprintf(
					"panel-browser-%d-%s@example.test",
					lifecycleID,
					role,
				),
				role,
			)
			if err := f.h.repo.Queries.AddProjectMember(t.Context(),
				db.AddProjectMemberParams{
					ProjectID: f.project.ID, UserID: user.ID, Role: "deployer",
				}); err != nil {
				t.Fatal(err)
			}
			if err := f.h.repo.Queries.AddProjectMember(t.Context(),
				db.AddProjectMemberParams{
					ProjectID: p.empty.ID, UserID: user.ID, Role: "deployer",
				}); err != nil {
				t.Fatal(err)
			}
			b.setBackTestSession(
				t,
				f.baseURL,
				memberRoleSession(t, f.h, user.ID),
			)
			for _, width := range []int{375, 1280} {
				t.Run(
					fmt.Sprintf("%d/%s/%d", lifecycleID, role, width),
					func(t *testing.T) {
						b.call(
							t,
							"Emulation.setDeviceMetricsOverride",
							map[string]any{
								"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
							},
							&struct{}{},
						)
						b.navigateBackTest(t, f.baseURL+"/projects")
						if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const link = document.querySelector('#projects-list article a[href="/projects/%d"]');
 const card = link?.closest('article');
 const grid = card?.querySelector('[data-project-environment-grid]');
 const untouched = document.querySelector('#projects-list article a[href="/projects/%d"]')?.closest('article');
 return !!grid && card.textContent.includes('own-attempt') &&
 !card.textContent.includes('No environments') && !card.textContent.includes('foreign-version') &&
 untouched?.textContent.includes('No deployments yet') &&
 grid.querySelector('.bg-error') !== null;
})()`, f.project.ID, p.empty.ID))) != "true" {
							t.Fatal(
								"rendered project card lost its failed attempt or environment",
							)
						}
						b.screenshot(
							t,
							fmt.Sprintf(
								"project-panel-list-%d-%s-%d",
								lifecycleID,
								role,
								width,
							),
						)
						b.navigateBackTest(
							t,
							fmt.Sprintf(
								"%s/projects/%d",
								f.baseURL,
								f.project.ID,
							),
						)
						if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const row = [...document.querySelectorAll('#project-detail tbody tr')]
   .find(row => row.querySelector('td.font-medium')?.textContent === %q);
 if (!row || !row.innerText.includes('own-success') || !row.innerText.includes('own-attempt') ||
 row.textContent.includes('No successful deploys') || row.textContent.includes('foreign-version')) return false;
 const dots = [...row.querySelectorAll('span.w-2')].map(dot => dot.title);
 return dots.join(',') === 'failed,succeeded' && row.querySelector('.badge')?.textContent.includes('failed') &&
 document.documentElement.scrollWidth <= innerWidth;
})()`, f.environment.Name))) != "true" {
							t.Fatal(
								"rendered project detail lost its own success, failure, or streak",
							)
						}
						b.screenshot(
							t,
							fmt.Sprintf(
								"project-panel-detail-%d-%s-%d",
								lifecycleID,
								role,
								width,
							),
						)
					},
				)
			}
		}
	}
}

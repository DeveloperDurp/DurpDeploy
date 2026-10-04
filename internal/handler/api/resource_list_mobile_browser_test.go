//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestResourceListMobileBrowserE2E(t *testing.T) {
	// Given: API-created lists with long values and a multi-stage project.
	f := newArtifactE2E(t)
	var lifecycle struct{ ID int64 }
	description := strings.Repeat("description-without-spaces-", 8)
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{
			"name":        strings.Repeat("mobile-lifecycle-", 5),
			"description": description,
		}, 201), &lifecycle)
	var project struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/projects",
		map[string]any{
			"name":         strings.Repeat("mobile-project-", 5),
			"description":  description,
			"lifecycle_id": lifecycle.ID,
		}, 201), &project)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST",
		fmt.Sprintf("/api/v1/projects/%d/releases", project.ID),
		map[string]string{"version": strings.Repeat("release-", 10)},
		201), &release)
	for i := range 4 {
		var environment struct{ ID int64 }
		decodeStepLogTest(t, f.api(t, "POST", "/api/v1/environments",
			map[string]string{
				"name": fmt.Sprintf(
					"%d-%s",
					i,
					strings.Repeat("environment-", 5),
				),
				"description": description,
				"tags":        strings.Repeat("long-tag-", 8),
			}, 201), &environment)
		f.api(t, "POST",
			fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
			map[string]int64{"environment_id": environment.ID}, 201)
		if i < 3 {
			seedDeployment(t, f.h.repo, release.ID, environment.ID, "succeeded")
		}
	}
	viewer := seedAPIUser(t, f.h.repo, "list-viewer@example.test", "viewer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: project.ID, UserID: viewer.ID, Role: "deployer",
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{
			ID: "resource-list-viewer", UserID: viewer.ID,
			CsrfToken: f.csrf, ExpiresAt: 4102444800,
		}); err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	for _, session := range []string{f.session, "resource-list-viewer"} {
		b.setBackTestSession(t, f.baseURL, session)
		for _, page := range []struct {
			path string
			id   int64
		}{
			{"projects", project.ID},
			{"environments", f.environment.ID},
			{"lifecycles", lifecycle.ID},
		} {
			// When: reading each public API and its web list as admin/viewer.
			var result struct{ Items []struct{ ID int64 } }
			decodeStepLogTest(t, f.api(t, "GET", "/api/v1/"+page.path,
				nil, 200), &result)
			found := false
			for _, item := range result.Items {
				found = found || item.ID == page.id
			}
			if !found {
				t.Fatalf("API list %s lost item %d", page.path, page.id)
			}
			b.navigateBackTest(t, f.baseURL+"/"+page.path)
			b.wait(t, `document.querySelector('main table tbody tr') !== null`)
			role := "admin"
			if session != f.session {
				role = "viewer"
			}
			// Then: fields stay readable, actions usable, and viewers read-only.
			b.captureNavigation(t, "mobile-"+page.path+"-"+role, func() {
				assertResourceListMobile(t, b)
				if role == "viewer" && string(b.evaluate(
					t,
					`!document.querySelector('main a[href$="/new"], main a[href$="/edit"], main button, main form') && ![...document.querySelectorAll('main a')].some(a => a.textContent === 'Edit')`,
				)) != "true" {
					t.Fatal("viewer sees write controls")
				}
			})
		}
	}
	// And: the project link still opens its detail through HTMX.
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, f.baseURL+"/projects")
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('main a[href="/projects/%d"]').click(); true`,
		project.ID))
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname === '/projects/%d' && document.activeElement?.id === 'page-content'`,
			project.ID,
		),
	)
	// And: row actions retain their navigation and fragment behavior.
	b.navigateBackTest(t, f.baseURL+"/environments")
	edit := fmt.Sprintf("/environments/%d/edit", f.environment.ID)
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('main a[href=%q]').click(); true`, edit))
	b.wait(t, `document.querySelector('main input[name="name"]') !== null`)
	b.navigateBackTest(t, f.baseURL+"/environments")
	b.evaluate(t, fmt.Sprintf(
		`window.confirm = () => true; document.querySelector('button[hx-delete="/environments/%d"]').click(); true`,
		f.environment.ID,
	))
	b.wait(t, fmt.Sprintf(
		`!document.querySelector('button[hx-delete="/environments/%d"]')`,
		f.environment.ID,
	))
	f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
		nil,
		404,
	)
	b.captureNavigation(t, "mobile-environments-deleted", func() {
		assertResourceListMobile(t, b)
	})
	b.navigateBackTest(t, f.baseURL+"/lifecycles")
	lifecyclePath := fmt.Sprintf("/lifecycles/%d", lifecycle.ID)
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('main a[href=%q]').click(); true`,
		lifecyclePath,
	))
	b.wait(t, `document.querySelector('main input[name="name"]') !== null`)
	b.navigateBackTest(t, f.baseURL+"/lifecycles")
	b.evaluate(t, `document.querySelector('main form button').click(); true`)
	b.wait(
		t,
		`document.readyState === 'complete' && window.Alpine && document.querySelector('main h1')?.textContent === 'Lifecycles' && !document.querySelector('main table')`,
	)
	f.api(t, "GET", "/api/v1"+lifecyclePath, nil, 404)
	b.captureNavigation(t, "mobile-lifecycles-empty")
}

func assertResourceListMobile(t *testing.T, b *packageBrowser) {
	t.Helper()
	if string(b.evaluate(t, `(() => {
 const table = document.querySelector('main table');
 const rows = [...table.tBodies[0].rows];
 const visible = el => el.getBoundingClientRect().height > 0;
 const cells = rows.flatMap(row => [...row.cells]);
 const controls = [...document.querySelectorAll('main a.btn, main button, main a.link')].filter(visible);
 const stageGrid = document.querySelector('[data-project-environment-grid]');
 const labels = [...table.querySelectorAll('span.md\\:hidden')];
 return innerWidth >= 768 ? getComputedStyle(rows[0]).display === 'table-row' && visible(table.tHead) &&
 cells.every(cell => cell.scrollWidth <= cell.clientWidth || getComputedStyle(cell).overflowX === 'hidden') :
 getComputedStyle(rows[0]).display === 'grid' && !visible(table.tHead) &&
 labels.length > 0 && labels.every(visible) && cells.every(cell => visible(cell) && cell.scrollWidth <= cell.clientWidth) &&
 controls.every(el => el.getBoundingClientRect().height >= 44) &&
 (!stageGrid || [...stageGrid.children].every(stage => stage.scrollWidth <= stage.clientWidth &&
 [...stage.children].every(el => el.scrollWidth <= el.clientWidth && el.scrollHeight <= el.clientHeight)));
})()`)) != "true" {
		b.screenshot(t, "resource-list-unusable")
		t.Fatal(
			"resource list hides or clips fields or has small phone controls",
		)
	}
}

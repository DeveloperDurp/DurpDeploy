//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func captureResourceLists(
	t *testing.T,
	f *artifactE2E,
	b *packageBrowser,
	session string,
	projectID, lifecycleID int64,
) {
	t.Helper()
	b.setBackTestSession(t, f.baseURL, session)
	role := "admin"
	if session != f.session {
		role = "viewer"
	}
	for _, page := range []struct {
		path string
		id   int64
	}{
		{"projects", projectID},
		{"environments", f.environment.ID},
		{"lifecycles", lifecycleID},
	} {
		// When: reading each public API and viewing the corresponding list.
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
		// Then: fields stay readable and only authorized Edit links appear.
		b.captureNavigation(t, "mobile-"+page.path+"-"+role, func() {
			assertResourceListMobile(t, b)
			if string(b.evaluate(
				t,
				`![...document.querySelectorAll('main button')].some(button => button.textContent.includes('Delete')) && !document.querySelector('main input[name="_method"][value="delete"]')`,
			)) != "true" {
				t.Fatal("resource list still exposes Delete")
			}
			if role == "viewer" && string(b.evaluate(
				t,
				`!document.querySelector('main a[href$="/new"], main a[href$="/edit"], main button, main form') && ![...document.querySelectorAll('main a')].some(a => a.textContent === 'Edit')`,
			)) != "true" {
				t.Fatal("viewer sees write controls")
			}
		})
	}
}

func assertEditDeleteControl(t *testing.T, b *packageBrowser, selector string) {
	t.Helper()
	if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const button = document.querySelector(%q);
 const save = document.querySelector('button[form="environment-settings-form"]');
 const back = document.querySelector('a[x-data="backNavigation"]');
 return button && button.getBoundingClientRect().height >= 44 &&
 button.closest('form')?.id !== 'lifecycle-settings-form' &&
 (location.pathname.startsWith('/lifecycles/') ||
 save?.textContent === 'Save' && back?.textContent === 'Back' &&
 ![...document.querySelectorAll('main a, main button')].some(el => ['Update', 'Cancel'].includes(el.textContent)));
})()`, selector))) != "true" {
		t.Fatal("edit Delete control is small or submits the settings form")
	}
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

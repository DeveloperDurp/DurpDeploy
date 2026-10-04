//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentListMobileBrowserE2E(t *testing.T) {
	// Given: public API data with long names and multiple pages of deployments.
	f := newArtifactE2E(t)
	var project struct{ ID int64 }
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		"/api/v1/projects",
		map[string]string{
			"name": strings.Repeat("mobile-project-", 5),
		},
		201,
	), &project)
	var release db.Release
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/releases", project.ID),
		map[string]string{
			"version": strings.Repeat("release-", 8),
		},
		201,
	), &release)
	for _, state := range []string{"running", "failed", "succeeded"} {
		seedDeployment(t, f.h.repo, release.ID, f.environment.ID, state)
	}
	var page struct {
		Items []struct{ ID int64 } `json:"items"`
		Total int64                `json:"total"`
	}
	decodeStepLogTest(t, f.api(t, "GET", "/api/v1/deployments?limit=2",
		nil, 200), &page)
	if len(page.Items) != 2 || page.Total != 3 {
		t.Fatal("public deployment list lost its pagination contract")
	}
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)

	// When: the list is opened on phones, tablets, and desktops.
	b.navigateBackTest(t, f.baseURL+"/deployments?limit=2")
	b.wait(t, `document.querySelectorAll('#deployments-tbody tr').length === 2`)
	b.captureNavigation(t, "mobile-deployment-list", func() {
		assertDeploymentListMobile(t, b)
	})

	// Then: Load more appends the same usable rows without duplicate IDs.
	b.evaluate(
		t,
		`document.querySelector('button[hx-target="#deployments-tbody"]').click(); true`,
	)
	b.wait(t, `document.querySelectorAll('#deployments-tbody tr').length === 3`)
	if string(b.evaluate(t, `(() => {
 const ids = [...document.querySelectorAll('[data-deployment-id]')].map(row => row.dataset.deploymentId);
 return new Set(ids).size === 3 && !document.querySelector('button[hx-target="#deployments-tbody"]');
})()`)) != "true" {
		t.Fatal("Load more duplicated rows or retained an exhausted button")
	}
	b.captureNavigation(t, "mobile-deployment-list-appended", func() {
		assertDeploymentListMobile(t, b)
	})
	b.evaluate(
		t,
		`document.querySelector('select[name="status"]').value = 'failed'; document.querySelector('form[action="/deployments"] button[type="submit"]').click(); true`,
	)
	b.wait(
		t,
		`document.readyState === 'complete' && window.Alpine && document.querySelectorAll('#deployments-tbody tr').length === 1`,
	)
	b.captureNavigation(t, "mobile-deployment-list-filtered")
	b.navigateBackTest(t, f.baseURL+"/deployments?status=pending")
	b.wait(t, `document.body.textContent.includes('No deployments yet.')`)
	b.captureNavigation(t, "mobile-deployment-list-empty")
}

func assertDeploymentListMobile(t *testing.T, b *packageBrowser) {
	t.Helper()
	if string(b.evaluate(t, `(() => {
 const row = document.querySelector('#deployments-tbody tr');
 const table = row.closest('table');
 const visible = el => el.getBoundingClientRect().height > 0;
 const buttons = [...document.querySelectorAll('main button, main a.btn, main select, main input')].filter(visible);
 const view = row.querySelector('a[href^="/deployments/"]');
 const download = row.querySelector('a[href$="/logs.txt"]');
 return download && !download.hasAttribute('hx-boost') && view.getAttribute('hx-boost') === 'true' &&
 (innerWidth >= 768 ? getComputedStyle(row).display === 'table-row' && visible(table.tHead) :
 getComputedStyle(row).display === 'grid' && !visible(table.tHead) &&
 [...row.cells].every(cell => cell.scrollWidth <= cell.clientWidth) &&
 buttons.every(el => el.getBoundingClientRect().height >= 44) &&
 [...row.querySelectorAll('span')].filter(el => el.classList.contains('md:hidden')).every(visible));
})()`)) != "true" {
		b.screenshot(t, "mobile-list-unusable")
		t.Fatal(
			"deployment list clips fields, hides labels, or shrinks phone controls",
		)
	}
}

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
	b.wait(
		t,
		`document.querySelectorAll('#deployments-tbody [data-deployment-id]').length === 2`,
	)
	b.captureNavigation(t, "mobile-deployment-list", func() {
		assertDeploymentListMobile(t, b)
	})

	// Then: Load more appends the same usable rows without duplicate IDs.
	b.evaluate(
		t,
		`document.querySelector('button[hx-target="#deployments-tbody"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelectorAll('#deployments-tbody [data-deployment-id]').length === 3`,
	)
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
		`document.readyState === 'complete' && window.Alpine && document.querySelectorAll('#deployments-tbody [data-deployment-id]').length === 1`,
	)
	b.captureNavigation(t, "mobile-deployment-list-filtered")
	b.navigateBackTest(t, f.baseURL+"/deployments?status=pending")
	b.wait(t, `document.body.textContent.includes('No deployments yet.')`)
	b.captureNavigation(t, "mobile-deployment-list-empty")
}

func assertDeploymentListMobile(t *testing.T, b *packageBrowser) {
	t.Helper()
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	if string(b.evaluate(t, `(() => {
 const row = document.querySelector('#deployments-tbody tr');
 const cards = [...document.querySelectorAll('#deployments-tbody [data-resource-card]')];
 const visible = el => el.getBoundingClientRect().height > 0;
 const buttons = [...document.querySelectorAll('main button, main a.btn, main select, main input')].filter(visible);
 return !document.querySelector('main table') && cards.length > 0 && cards.every(card => {
 const view = card.querySelector('a');
 const box = card.getBoundingClientRect();
 const fields = [...card.querySelectorAll('dd')];
 return view && view.getAttribute('hx-boost') === 'true' && box.height >= 44 &&
 Math.abs(view.getBoundingClientRect().width - box.width) <= 2 &&
 Math.abs(view.getBoundingClientRect().height - box.height) <= 2 &&
 fields.length === 4 && fields.every(el => visible(el) && el.scrollWidth <= el.clientWidth);
 }) && (innerWidth >= 768 || buttons.every(el => el.getBoundingClientRect().height >= 44));
})()`)) != "true" {
		b.screenshot(t, "mobile-list-unusable")
		t.Fatal(
			"deployment list clips fields, hides labels, or shrinks phone controls",
		)
	}
}

//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestHomeMobileBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("/api/v1/projects/%d", f.project.ID),
		nil,
		204,
	)
	f.api(
		t,
		"DELETE",
		fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
		nil,
		204,
	)
	b.navigateBackTest(t, f.baseURL+"/")
	if string(
		b.evaluate(
			t,
			`document.querySelector('main a[href="/projects/new"], main a[href="/environments/new"]') === null`,
		),
	) != "true" {
		t.Fatal("home still shows creation actions")
	}
	b.captureNavigation(t, "home-welcome")
	var project, environment struct{ ID int64 }
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		"/api/v1/projects",
		map[string]string{
			"name": strings.Repeat("home-project-", 6),
		},
		201,
	), &project)
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		"/api/v1/environments",
		map[string]string{
			"name": strings.Repeat("home-environment-", 6),
		},
		201,
	), &environment)
	b.navigateBackTest(t, f.baseURL+"/")
	b.wait(
		t,
		`document.querySelector('[data-home-charts]') && Alpine.$data(document.querySelector('[data-home-charts]')).empty`,
	)
	b.captureNavigation(t, "home-empty-deployments")
	var release db.Release
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/projects/%d/releases", project.ID),
		map[string]string{
			"version": strings.Repeat("home-release-", 8),
		},
		201,
	), &release)
	for _, status := range []string{"succeeded", "failed", "running"} {
		seedDeployment(t, f.h.repo, release.ID, environment.ID, status)
	}
	var deployments struct{ Total int64 }
	decodeStepLogTest(
		t,
		f.api(t, "GET", "/api/v1/deployments", nil, 200),
		&deployments,
	)
	if deployments.Total != 3 {
		t.Fatal("public deployment API lost dashboard data")
	}
	var activity []struct {
		Date   string           `json:"date"`
		Counts map[string]int64 `json:"counts"`
	}
	decodeStepLogTest(
		t,
		f.api(t, "GET", "/api/v1/deployments/activity", nil, 200),
		&activity,
	)
	if len(activity) != 14 || activity[13].Counts["succeeded"] != 1 ||
		activity[13].Counts["failed"] != 1 || activity[13].Counts["running"] != 1 {
		t.Fatalf("dashboard API counts: %+v", activity)
	}
	b.navigateBackTest(t, f.baseURL+"/")
	b.wait(
		t,
		`document.querySelectorAll('[data-home-charts] canvas').length === 2 && [...document.querySelectorAll('[data-home-charts] canvas')].every(c => DashboardChart.getChart(c)?.width > 0)`,
	)
	b.captureNavigation(t, "home-populated", func() {
		b.wait(t, `(() => {
 const section = document.querySelector('[data-home-charts]');
 const ink = getComputedStyle(section).color;
 return [...section.querySelectorAll('canvas')].every(canvas => {
  const chart = DashboardChart.getChart(canvas);
  return chart && chart.options.color === ink && chart.width <= canvas.parentElement.clientWidth &&
   chart.data.datasets.flatMap(d => d.data).reduce((a,b) => a+b, 0) === 3;
 }) && section.querySelectorAll('[data-chart-totals] dd').length === 3;
})()`)
		if string(b.evaluate(t, `(() => {
 const tables = [...document.querySelectorAll('[data-home-deployments]')];
 const summary = document.querySelector('[data-home-summary]');
 const columns = getComputedStyle(summary).gridTemplateColumns.split(' ').length;
 return tables.length === 3 && columns === (innerWidth < 1024 ? 2 : 4) &&
 tables.every(table => [...table.tBodies[0].rows].every(row =>
 innerWidth < 768 ? getComputedStyle(row).display === 'grid' && row.cells.length === 5 &&
 [...row.cells].every(cell => cell.getClientRects().length && cell.scrollWidth <= cell.clientWidth) &&
 row.querySelectorAll('span.md\\:hidden').length === 5 :
 getComputedStyle(row).display === 'table-row' && table.tHead.getClientRects().length)) &&
 !document.querySelector('main a[href="/projects/new"], main a[href="/environments/new"]');
})()`)) != "true" {
			b.screenshot(t, "home-mobile-layout-failure")
			t.Fatal(
				"home dashboard hides fields or has unusable phone geometry",
			)
		}
	})
	// HTMX navigation destroys old charts and creates one fresh pair on return.
	b.evaluate(
		t,
		`window.oldHomeCharts = [...document.querySelectorAll('[data-home-charts] canvas')].map(c => DashboardChart.getChart(c)); document.querySelector('#app-navbar a[href="/projects"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/projects' && !document.querySelector('[data-home-charts]')`,
	)
	b.wait(
		t,
		`oldHomeCharts.every(c => c.ctx === null) && Object.keys(DashboardChart.instances).length === 0`,
	)
	b.wait(
		t,
		`document.querySelector('#app-navbar a[href="/"]').getAttribute('hx-boost') === 'true' && !document.querySelector('.htmx-settling')`,
	)
	b.evaluate(
		t,
		`document.querySelector('#app-navbar a[href="/"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/' && window.DashboardChart && Object.keys(DashboardChart.instances).length === 2`,
	)
}

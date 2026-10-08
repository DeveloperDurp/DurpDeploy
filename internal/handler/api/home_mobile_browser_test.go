//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
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
	for _, status := range []string{"succeeded", "failed", "running", "pending_approval", "awaiting_artifact_approval", "queued"} {
		seedDeployment(t, f.h.repo, release.ID, environment.ID, status)
	}
	var deployments struct{ Total int64 }
	decodeStepLogTest(
		t,
		f.api(t, "GET", "/api/v1/deployments", nil, 200),
		&deployments,
	)
	if deployments.Total != 6 {
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
	if activity[13].Counts["pending_approval"] != 1 ||
		activity[13].Counts["awaiting_artifact_approval"] != 1 || activity[13].Counts["queued"] != 1 {
		t.Fatalf("public activity API lost approval states: %+v", activity)
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
   chart.data.datasets.flatMap(d => d.data).reduce((a,b) => a+b, 0) === 6;
 }) && section.querySelectorAll('[data-chart-totals] dd').length === 6;
})()`)
		if string(b.evaluate(t, `(() => {
 const tables = [...document.querySelectorAll('[data-home-deployments]')];
 const summary = document.querySelector('[data-home-summary]');
 const columns = getComputedStyle(summary).gridTemplateColumns.split(' ').length;
 const waiting = [...document.querySelectorAll('#home-waiting .badge')].map(el => el.textContent);
 const running = [...document.querySelectorAll('#home-running .badge')].map(el => el.textContent);
 return !document.querySelector('[data-home-charts] details') && document.querySelector('#home-daily-counts').classList.contains('sr-only') &&
 waiting.length === 3 && waiting.includes('queued') && waiting.includes('pending_approval') && waiting.includes('awaiting_artifact_approval') &&
 running.length === 1 && running[0] === 'running' && document.querySelector('#home-running-count .stat-value').textContent === '1' &&
 tables.length === 4 && columns === (innerWidth < 1024 ? 2 : 4) &&
 tables.every(table => [...table.tBodies[0].rows].every(row => {
 const link = row.querySelector('a');
 const box = row.getBoundingClientRect();
 const target = link?.getBoundingClientRect();
 return link?.getAttribute('href').startsWith('/deployments/') && !link.classList.contains('btn') &&
 Math.abs(target.width - box.width) < 2 && Math.abs(target.height - box.height) < 2 &&
 getComputedStyle(row).borderRadius === (innerWidth < 768 ? getComputedStyle(link).borderRadius : '0px');
 })) &&
 tables.every(table => [...table.tBodies[0].rows].every(row =>
 innerWidth < 768 ? getComputedStyle(row).display === 'grid' && row.cells.length === 5 &&
 getComputedStyle(row).borderRadius !== '0px' && parseFloat(getComputedStyle(row).marginBottom) >= 12 &&
 getComputedStyle(row).backgroundColor !== 'rgba(0, 0, 0, 0)' &&
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
	// A plain cell click follows the row's native deployment link.
	var target struct {
		Href string
		X, Y float64
	}
	decodeStepLogTest(t, b.evaluate(t, `(() => {
 const row = document.querySelector('#home-recent tbody tr');
 const cell = row.cells[4].getBoundingClientRect();
 window.rowNavigationSentinel = true;
 return {Href:row.querySelector('a').getAttribute('href'),X:cell.x+cell.width/2,Y:cell.y+cell.height/2};
})()`), &target)
	f.api(t, "GET", "/api/v1"+target.Href, nil, 200)
	b.evaluate(
		t,
		`document.querySelector('#home-recent tbody tr').scrollIntoView(); true`,
	)
	decodeStepLogTest(t, b.evaluate(t, `(() => {
 const row = document.querySelector('#home-recent tbody tr');
 const cell = row.cells[4].getBoundingClientRect();
 return {Href:row.querySelector('a').getAttribute('href'),X:cell.x+cell.width/2,Y:cell.y+cell.height/2};
})()`), &target)
	b.call(t, "Input.dispatchMouseEvent", map[string]any{
		"type": "mouseMoved", "x": target.X, "y": target.Y,
	}, &struct{}{})
	b.wait(t, `(() => {
 const link = document.querySelector('#home-recent tbody tr a');
 const style = getComputedStyle(link);
 return link.matches(':hover') && style.cursor === 'pointer' &&
 style.backgroundColor !== 'rgba(0, 0, 0, 0)' && style.boxShadow !== 'none' &&
 document.getAnimations().every(a => a.playState !== 'running');
})()`)
	b.screenshot(t, "home-deployment-row-highlight")
	// Capturing the full page restores scroll to the top. Locate the row
	// again before sending viewport coordinates for the actual click.
	decodeStepLogTest(t, b.evaluate(t, `(() => {
 const row = document.querySelector('#home-recent tbody tr');
 row.scrollIntoView();
 const cell = row.cells[4].getBoundingClientRect();
 return {Href:row.querySelector('a').getAttribute('href'),X:cell.x+cell.width/2,Y:cell.y+cell.height/2};
})()`), &target)
	for _, kind := range []string{"mousePressed", "mouseReleased"} {
		b.call(t, "Input.dispatchMouseEvent", map[string]any{
			"type": kind, "x": target.X, "y": target.Y,
			"button": "left", "clickCount": 1,
		}, &struct{}{})
	}
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname === %q && window.rowNavigationSentinel === true`,
			target.Href,
		),
	)
	b.navigateBackTest(t, f.baseURL+"/")
	b.evaluate(
		t,
		`document.querySelector('#home-recent tbody tr a').focus(); true`,
	)
	b.call(t, "Input.dispatchKeyEvent", map[string]any{
		"type": "keyDown", "key": "Enter", "code": "Enter",
		"windowsVirtualKeyCode": 13,
	}, &struct{}{})
	b.wait(t, fmt.Sprintf(`location.pathname === %q`, target.Href))
	b.navigateBackTest(t, f.baseURL+"/")
	b.wait(
		t,
		`window.DashboardChart && Object.keys(DashboardChart.instances).length === 2`,
	)
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

func TestHomeRunningPollBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 375, "height": 900, "deviceScaleFactor": 1, "mobile": false,
	}, &struct{}{})
	b.navigateBackTest(t, f.baseURL+"/")
	b.wait(
		t,
		`window.htmx && document.querySelector('#home-running').textContent.includes('No deployments in progress.')`,
	)
	b.evaluate(
		t,
		`window.originalMain = document.querySelector('#page-content'); window.originalCharts = document.querySelector('[data-home-charts]'); window.originalSummaryCards = [...document.querySelector('[data-home-summary]').children]; true`,
	)
	b.evaluate(t, `(() => {
 const start = document.startViewTransition.bind(document);
 window.homeTransitionCount = 0;
 document.startViewTransition = update => {
  homeTransitionCount++;
  return start(update);
 };
 return true;
})()`)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Refresh check", "script_body": "sleep 8",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/releases",
			map[string]string{"version": "live-home"},
			201,
		),
		&release,
	)
	var deployment db.Deployment
	decodeStepLogTest(
		t,
		f.api(t, "POST", f.base()+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 201),
		&deployment,
	)
	b.wait(
		t,
		`document.querySelector('#home-running').textContent.includes('live-home') && document.querySelector('#home-running-count .stat-value').textContent === '1'`,
	)
	b.wait(t, `!document.querySelector('#home-deployments.htmx-settling')`)
	b.wait(
		t,
		`document.querySelector('#home-latest .badge').textContent === 'running' && document.querySelector('#home-recent .badge').textContent === 'running' && document.querySelector('#home-today-count .stat-value').textContent === '1' && Object.keys(DashboardChart.instances).length === 2`,
	)
	b.evaluate(
		t,
		`window.originalChartInstances = [...document.querySelectorAll('[data-home-charts] canvas')].map(c => DashboardChart.getChart(c)); true`,
	)
	b.screenshot(t, "home-running-poll-375")
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	var stored db.Deployment
	decodeStepLogTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
			nil,
			200,
		),
		&stored,
	)
	if stored.Status != "succeeded" {
		t.Fatalf("public API status=%s", stored.Status)
	}
	b.wait(
		t,
		`document.querySelector('#home-running').textContent.includes('No deployments in progress.') && document.querySelector('#home-running-count .stat-value').textContent === '0'`,
	)
	b.wait(
		t,
		`document.querySelector('#home-latest .badge').textContent === 'succeeded' && document.querySelector('#home-recent .badge').textContent === 'succeeded' && Alpine.$data(originalCharts).totals.some(item => item.status === 'succeeded' && item.count === 1) && !Alpine.$data(originalCharts).totals.some(item => item.status === 'running')`,
	)
	b.screenshot(t, "home-completed-poll-375")
	if string(b.evaluate(t, `(async () => {
 await Alpine.nextTick();
 let updates = 0;
 const originals = originalChartInstances.map(chart => chart.update);
 originalChartInstances.forEach(chart => {
  chart.update = function(...args) { updates++; return originals[originalChartInstances.indexOf(chart)].apply(this, args); };
 });
 try {
  await Alpine.$data(originalCharts).load();
  return updates === 0;
 } finally {
  originalChartInstances.forEach((chart, index) => { chart.update = originals[index]; });
 }
})()`)) != "true" {
		t.Fatal("unchanged activity repainted the charts")
	}
	if string(
		b.evaluate(
			t,
			`originalMain === document.querySelector('#page-content') && originalCharts === document.querySelector('[data-home-charts]') && originalSummaryCards.every(c => c.isConnected) && originalChartInstances.every(c => c.ctx !== null && DashboardChart.getChart(c.canvas) === c) && document.querySelector('#home-deployments').getAttribute('hx-trigger') === 'every 5s'`,
		),
	) != "true" {
		t.Fatal(
			"running refresh replaced the page/charts or changed its interval",
		)
	}
	// Approval waits appear on the next poll without counting as running.
	var lifecycle struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Home approval"}, 201), &lifecycle)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
		map[string]any{
			"environment_id":    f.environment.ID,
			"sort_order":        1,
			"requires_approval": true,
		},
		201,
	)
	var waiting db.Deployment
	decodeStepLogTest(
		t,
		f.api(t, "POST", f.base()+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 201),
		&waiting,
	)
	if waiting.Status != "pending_approval" {
		t.Fatalf("public API approval status=%s", waiting.Status)
	}
	b.wait(
		t,
		`document.querySelector('#home-waiting .badge')?.textContent === 'pending_approval' && document.querySelector('#home-running-count .stat-value').textContent === '0'`,
	)
	b.screenshot(t, "home-waiting-approval-375")
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/approve", waiting.ID),
		nil,
		200,
	)
	b.wait(
		t,
		`!document.querySelector('#home-waiting .badge') && document.querySelector('#home-running .badge')?.textContent === 'running' && document.querySelector('#home-running-count .stat-value').textContent === '1'`,
	)
	f.completion(t, waiting.ID, events.DeploymentSucceeded)
	b.wait(
		t,
		`!document.querySelector('#home-waiting .badge') && !document.querySelector('#home-running .badge') && document.querySelector('#home-running-count .stat-value').textContent === '0'`,
	)
	if string(
		b.evaluate(
			t,
			`homeTransitionCount > 0 && getComputedStyle(document.documentElement).viewTransitionName === 'none' && getComputedStyle(document.querySelector('#home-waiting')).viewTransitionName === 'home-waiting'`,
		),
	) != "true" {
		t.Fatal("dashboard refresh did not use scoped view transitions")
	}
	b.call(t, "Emulation.setEmulatedMedia", map[string]any{
		"features": []map[string]string{
			{"name": "prefers-reduced-motion", "value": "reduce"},
		},
	}, &struct{}{})
	if string(b.evaluate(t, `(async () => {
 const count = homeTransitionCount;
 const source = document.querySelector('#home-deployments');
 await htmx.ajax('GET', '/dashboard/deployments', { source, target: source, swap: 'outerHTML transition:true' });
 return matchMedia('(prefers-reduced-motion: reduce)').matches && homeTransitionCount === count && document.querySelector('#home-deployments');
})().then(Boolean)`)) != "true" {
		t.Fatal("reduced-motion dashboard refresh animated or lost its content")
	}
}

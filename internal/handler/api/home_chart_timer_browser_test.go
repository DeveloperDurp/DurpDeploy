//go:build e2e && packagebrowser

package api_test

import "testing"

func TestHomeChartTimerBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.call(t, "Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source": `(() => {
 const interval = window.setInterval, clear = window.clearInterval, fetch = window.fetch;
 window.chartRequests = 0;
 window.chartTimers = new Map();
 window.setInterval = (callback, delay, ...args) => {
  const id = interval(callback, delay, ...args);
  if (delay === 30000) chartTimers.set(id, callback);
  return id;
 };
 window.clearInterval = id => { chartTimers.delete(id); clear(id); };
 window.fetch = (...args) => {
  if (args[0] === '/dashboard/activity') chartRequests++;
  return fetch(...args);
 };
})()`,
	}, &struct{}{})
	b.navigateBackTest(t, f.baseURL+"/")
	b.wait(
		t,
		`chartRequests === 1 && chartTimers.size === 1 && Alpine.$data(document.querySelector('[data-home-charts]')).empty`,
	)
	// The five-second panel event no longer triggers aggregate queries.
	if string(b.evaluate(t, `(() => {
 for (let count = 0; count < 6; count++) dispatchEvent(new Event('home-deployments-refreshed'));
 return chartRequests === 1;
})()`)) != "true" {
		t.Fatal("deployment refresh still triggers chart requests")
	}
	b.evaluate(t, `[...chartTimers.values()][0](); true`)
	b.wait(t, `chartRequests === 2`)
	b.wait(
		t,
		`document.querySelector('#app-navbar a[href="/projects"]').getAttribute('hx-boost') === 'true' && !document.querySelector('.htmx-settling')`,
	)
	b.evaluate(
		t,
		`document.querySelector('#app-navbar a[href="/projects"]').click(); true`,
	)
	b.wait(t, `location.pathname === '/projects' && chartTimers.size === 0`)
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
		`location.pathname === '/' && chartTimers.size === 1 && chartRequests === 3`,
	)
}

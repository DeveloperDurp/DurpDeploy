//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBackHistoryBrowserE2E(t *testing.T) {
	// Given: a fresh tab, without a preceding page.
	f := newArtifactE2E(t)
	browser := startPackageBrowser(t)
	browser.setBackTestSession(t, f.baseURL, f.session)
	project := fmt.Sprintf("%s/projects/%d", f.baseURL, f.project.ID)
	repository := project + "/package-repository"
	browser.navigateBackTest(t, repository)
	browser.call(t, "Page.resetNavigationHistory", struct{}{}, &struct{}{})
	if string(browser.evaluate(t, "history.length === 1")) != "true" {
		t.Fatal("fresh test tab must have no previous entry")
	}
	// When / Then: a direct link uses the project fallback.
	browser.clickBackTest(t)
	browser.waitBackTestURL(t, project)
	// Return to the first entry: total history still includes the forward page.
	browser.evaluate(t, "history.back(); true")
	browser.waitBackTestURL(t, repository)
	if string(browser.evaluate(
		t,
		"history.length > 1 && !navigation.canGoBack && navigation.canGoForward",
	)) != "true" {
		t.Fatal("expected first entry with forward history")
	}
	browser.clickBackTest(t)
	browser.waitBackTestURL(t, project)

	// Given: several pages in sequence, including a filtered entry URL.
	previous := f.baseURL + "/projects?limit=20&offset=0"
	browser.navigateBackTest(t, previous)
	browser.navigateBackTest(t, project)
	browser.navigateBackTest(t, project+"/steps-page")
	// When / Then: repeated Back moves backwards, without creating a loop.
	browser.clickBackTest(t)
	browser.waitBackTestURL(t, project)
	browser.clickBackTest(t)
	browser.waitBackTestURL(t, previous)

	// When / Then: a reload preserves Back's destination.
	browser.navigateBackTest(t, project)
	browser.wire.events = nil
	browser.call(t, "Page.reload", struct{}{}, &struct{}{})
	if err := browser.wire.waitEvent(
		"Page.frameStoppedLoading", browser.session,
	); err != nil {
		t.Fatal(err)
	}
	browser.waitBackTestURL(t, project)
	browser.clickBackTest(t)
	browser.waitBackTestURL(t, previous)

	// Older browsers still use real history when Navigation is unavailable.
	browser.navigateBackTest(t, project)
	browser.evaluate(t,
		"Object.defineProperty(window, 'navigation', {value: undefined}); true",
	)
	browser.clickBackTest(t)
	browser.waitBackTestURL(t, previous)
	// Modified activation remains normal link behavior.
	browser.navigateBackTest(t, project)
	if string(browser.evaluate(
		t,
		`document.querySelector('a[x-data=backNavigation]').dispatchEvent(new MouseEvent('click', {bubbles: true, cancelable: true, ctrlKey: true}))`,
	)) != "true" {
		t.Fatal("modified link activation was intercepted")
	}
	browser.waitBackTestURL(t, project)

	// Cross-origin history must not be mistaken for an empty tab.
	external := httptest.NewServer(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			if _, err := fmt.Fprintf(w,
				`<h1>External page</h1><a href="%s">Open repository</a>`,
				repository,
			); err != nil {
				t.Error(err)
			}
		},
	))
	defer external.Close()
	browser.call(t, "Page.navigate", map[string]string{
		"url": external.URL,
	}, &struct{}{})
	browser.wait(t, fmt.Sprintf(
		`location.href === %q && document.readyState === 'complete'`,
		external.URL+"/",
	))
	browser.evaluate(t, "document.querySelector('a').click(); true")
	browser.waitBackTestURL(t, repository)
	browser.clickBackTest(t)
	browser.wait(t, fmt.Sprintf(
		`location.href === %q && document.querySelector('h1').textContent === 'External page'`,
		external.URL+"/",
	))
}

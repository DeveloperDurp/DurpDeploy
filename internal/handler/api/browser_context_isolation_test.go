//go:build e2e && (packagebrowser || agentbrowser)

package api_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestBrowserContextIsolationE2E(t *testing.T) {
	// Given: two tests reuse a Chromium process at the same application origin.
	t.Setenv("DURPDEPLOY_TEST_BROWSER_ENDPOINT", packageBrowserEndpoint(t))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter,
		r *http.Request,
	) {
		_, _ = w.Write(
			[]byte("<!doctype html><title>Context isolation</title>"),
		)
	}))
	t.Cleanup(server.Close)
	first := startPackageBrowser(t)
	first.evaluate(t, "location.href="+strconv.Quote(server.URL))
	first.wait(t, "location.origin === "+strconv.Quote(server.URL))
	first.evaluate(t, `localStorage.setItem('fixture', 'private');
		document.cookie = 'fixture=private'; true`)
	second := startPackageBrowser(t)
	second.evaluate(t, "location.href="+strconv.Quote(server.URL))
	second.wait(t, "location.origin === "+strconv.Quote(server.URL))
	// Then: cookies and local storage do not cross context boundaries.
	if string(second.evaluate(
		t,
		`document.cookie === '' && localStorage.getItem('fixture') === null`,
	)) != "true" {
		t.Fatal("shared Chromium leaked browser state between test contexts")
	}
}

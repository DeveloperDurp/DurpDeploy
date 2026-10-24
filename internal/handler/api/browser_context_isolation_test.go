//go:build e2e && (packagebrowser || agentbrowser)

package api_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
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
	first := startLivePackageBrowser(t, server.URL)
	first.evaluate(t, "location.href="+strconv.Quote(server.URL))
	first.wait(t, "location.origin === "+strconv.Quote(server.URL))
	first.evaluate(t, `localStorage.setItem('fixture', 'private');
		document.cookie = 'fixture=private'; true`)
	second := startLivePackageBrowser(t, server.URL)
	second.evaluate(t, "location.href="+strconv.Quote(server.URL))
	second.wait(t, "location.origin === "+strconv.Quote(server.URL))
	// Then: cookies and local storage do not cross context boundaries.
	if string(second.evaluate(
		t,
		`document.cookie === '' && localStorage.getItem('fixture') === null`,
	)) != "true" {
		t.Fatal("shared Chromium leaked browser state between test contexts")
	}
	// A self-signed HTTPS override must not affect the shared HTTP browser.
	secure := httptest.NewTLSServer(server.Config.Handler)
	t.Cleanup(secure.Close)
	private := startLivePackageBrowser(t, secure.URL)
	private.evaluate(t, "location.href="+strconv.Quote(secure.URL))
	private.wait(t, "location.origin === "+strconv.Quote(secure.URL))
	var navigation struct {
		ErrorText string `json:"errorText"`
	}
	first.call(t, "Page.navigate", map[string]string{"url": secure.URL},
		&navigation)
	if !strings.HasPrefix(navigation.ErrorText, "net::ERR_CERT_") {
		t.Fatalf("shared browser accepted a private certificate override: %+v",
			navigation)
	}
}

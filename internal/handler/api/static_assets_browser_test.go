//go:build e2e && packagebrowser

package api_test

import (
	"strings"
	"testing"
)

func TestStaticAssetsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, path := range []string{"/", "/projects", "/environments"} {
		b.navigateBackTest(t, f.baseURL+path)
		b.wait(t, `window.Alpine && window.htmx &&
 Array.from(document.querySelectorAll('link[rel="stylesheet"]'))
 .filter(link => link.href.includes('/static/')).every(link => link.sheet) &&
 document.querySelector('link[href*="/static/css/"]')`)
		b.screenshot(t, "static-assets-"+strings.ReplaceAll(path, "/", "home"))
	}
	b.call(t, "Page.navigate", map[string]string{
		"url": f.baseURL + "/api/swagger/",
	}, &struct{}{})
	b.wait(t, `window.ui && window.SwaggerUIBundle &&
 document.querySelector('#swagger-ui .swagger-ui')`)
	b.screenshot(t, "static-assets-swagger")
}

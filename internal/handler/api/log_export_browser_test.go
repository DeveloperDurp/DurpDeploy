//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTextLogExportBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, target := range seedTextExportTargets(t, f) {
		expected := textExportHeader(t, f, target.deploymentID) +
			seedTextExportLogs(t, f, target.deploymentID) + "\n"
		b.navigateBackTest(t, f.baseURL+
			strings.TrimSuffix(target.webPath, "/logs.txt"))
		b.wait(t, `document.querySelector('a[href$="/logs.txt"]')`)
		var result struct {
			Path, ContentType, Disposition, Body string
			Status                               int
			Boosted                              bool
		}
		if err := json.Unmarshal(b.evaluate(t, `(async () => {
 const link = document.querySelector('a[href$="/logs.txt"]');
 const response = await fetch(link.href);
 return {Path:new URL(link.href).pathname, Boosted:link.getAttribute('hx-boost')==='true',
  Status:response.status, ContentType:response.headers.get('Content-Type'),
  Disposition:response.headers.get('Content-Disposition'), Body:await response.text()};
})()`), &result); err != nil {
			t.Fatal(err)
		}
		if result.Path != target.webPath || result.Boosted ||
			result.Status != 200 || result.Body != expected ||
			result.ContentType != "text/plain; charset=utf-8" ||
			result.Disposition != fmt.Sprintf(
				`attachment; filename="deployment-%d.log"`,
				target.deploymentID,
			) {
			t.Fatalf("rendered export link changed for %s", target.webPath)
		}
		if string(f.api(t, "GET", target.apiPath, nil, 200)) != result.Body {
			t.Fatal("browser download differs from the public API")
		}
	}
}

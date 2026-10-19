//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"net/http/httptest"
	"testing"
)

func TestSwaggerInstanceBrowserE2E(t *testing.T) {
	f := newPermissionFixture(t)
	project := seedProject(t, f.h.repo)
	user := seedAPIUser(t, f.h.repo, "swagger@example.test", "admin")
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	proxy := newSwaggerTLSProxy(t, f.server.URL)
	b := startPackageBrowser(t, true)
	// Ignore only the isolated test proxy's self-signed certificate.
	b.call(t, "Security.setIgnoreCertificateErrors",
		map[string]bool{"ignore": true}, &struct{}{})
	for _, instance := range []*httptest.Server{f.server, proxy} {
		t.Run(instance.URL, func(t *testing.T) {
			scheme := "http"
			if instance == proxy {
				scheme = "https"
			}
			b.call(t, "Page.navigate", map[string]string{
				"url": instance.URL + "/api/swagger/",
			}, &struct{}{})
			b.wait(
				t,
				`window.ui && document.querySelector('#operations-health-healthzAPI')`,
			)
			if string(b.evaluate(t, fmt.Sprintf(`(() => {
const target = document.querySelector('#api-target');
return target?.getClientRects().length > 0 &&
 target.textContent === %q &&
 window.ui.authSelectors.authorized().size === 0;
})()`, instance.URL+"/api/v1"))) != "true" {
				t.Fatal(
					"current API target is not visible before authorization",
				)
			}
			b.screenshot(t, "swagger-target-"+scheme)
			execute := func(operation, path string, status int) {
				t.Helper()
				selector := "#operations-" + operation
				b.evaluate(t, fmt.Sprintf(`(() => {
const operation = document.querySelector(%q);
if (!operation.classList.contains('is-open'))
 operation.querySelector('.opblock-summary-control').click();
return true;
})()`, selector))
				b.wait(t, fmt.Sprintf(`document.querySelector(%q)`,
					selector+" .try-out__btn"))
				b.evaluate(t, fmt.Sprintf(`(() => {
const operation = document.querySelector(%q);
if (!operation.querySelector('.execute'))
 operation.querySelector('.try-out__btn').click();
return true;
})()`, selector))
				b.wait(t, fmt.Sprintf(`document.querySelector(%q)`,
					selector+" .execute"))
				b.evaluate(
					t,
					fmt.Sprintf(`document.querySelector(%q).click(); true`,
						selector+" .execute"),
				)
				b.wait(t, fmt.Sprintf(`(() => {
const operation = document.querySelector(%q);
return operation.querySelector('.live-responses-table tbody .response-col_status')
 ?.textContent.trim() === %q &&
 operation.querySelector('.request-url pre')?.textContent.trim() === %q;
})()`, selector, fmt.Sprint(status), instance.URL+"/api/v1"+path))
				t.Logf("Swagger GET %s/api/v1%s returned %d",
					instance.URL, path, status)
			}
			execute("health-healthzAPI", "/healthz", 200)
			b.wait(
				t,
				`document.querySelector('#operations-health-healthzAPI .live-responses-table .microlight')?.textContent.includes('"ok": true')`,
			)
			b.screenshot(t, "swagger-health-"+scheme)
			execute("projects-listProjects", "/projects", 401)
			// Exercise the unchanged raw-token interceptor without logging secrets.
			b.evaluate(
				t,
				fmt.Sprintf(`window.ui.authActions.authorize({bearer: {
name: 'bearer', schema: {type: 'apiKey', in: 'header', name: 'Authorization'},
value: %q}}); true`, token),
			)
			execute("projects-listProjects", "/projects", 200)
			b.wait(
				t,
				fmt.Sprintf(
					`document.querySelector('#operations-projects-listProjects .live-responses-table .microlight')?.textContent.includes(%q)`,
					project.Name,
				),
			)
			b.evaluate(t, `window.ui.authActions.logout(['bearer']); true`)
		})
	}
}

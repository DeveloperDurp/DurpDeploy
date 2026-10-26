//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestConfiguredArtifactSafetyBrowserE2E(t *testing.T) {
	for _, field := range []string{"bytes", "files", "nodes", "metadata"} {
		for _, accepted := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/%t", field, accepted), func(t *testing.T) {
				f := newArtifactSafetyFixture(t, field, accepted)
				b := startPackageBrowser(t)
				b.setBackTestSession(t, f.baseURL, f.session)
				b.navigateBackTest(
					t,
					fmt.Sprintf(
						"%s/projects/%d/package-repository",
						f.baseURL,
						f.project.ID,
					),
				)
				b.wait(
					t,
					`window.htmx && document.querySelector('[name=version]') && document.readyState === 'complete'`,
				)
				b.evaluate(
					t,
					`(()=>{const i=document.querySelector('[name=version]');i.value='1';i.form.requestSubmit();return true})()`,
				)
				message := "Could not retrieve and validate this ZIP"
				if accepted {
					message = "ZIP validation passed"
				}
				b.wait(
					t,
					fmt.Sprintf(
						`document.querySelector('#package-test-result').innerText.includes(%q) && !document.querySelector('.htmx-request')`,
						message,
					),
				)
				b.screenshot(
					t,
					fmt.Sprintf(
						"configured-artifact-safety-%s-%t",
						field,
						accepted,
					),
				)
			})
		}
	}
}

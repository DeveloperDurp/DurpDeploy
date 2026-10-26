//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestConfiguredArtifactDownloadBrowserE2E(t *testing.T) {
	for _, limit := range []int64{1024, 2048} {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			f := newArtifactDownloadFixture(t, limit)
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
				`(() => {const input=document.querySelector('[name=version]');input.value='1';input.form.requestSubmit();return true})()`,
			)
			message := "ZIP validation passed"
			if limit == 1024 {
				message = "Could not retrieve and validate this ZIP"
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
				fmt.Sprintf("configured-artifact-download-%d", limit),
			)
		})
	}
}

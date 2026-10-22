//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestReleaseProvenanceBrowserE2E(t *testing.T) {
	f, _ := newProvenanceE2E(t, provenanceDSN(t, "SQLite"))
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	path := strings.TrimPrefix(f.base(), "/api/v1") + "/releases"
	source := publicSource()
	for _, width := range []int{375, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 844, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+path)
		version := fmt.Sprintf("browser-%d", width)
		b.evaluate(t, fmt.Sprintf(`(() => {
 const form = document.querySelector('#release-version').form;
 for (const [name,value] of Object.entries({version:%q,repository_url:%q,commit_sha:%q,artifact_ref:%q,ci_run_url:%q})) form.elements[name].value=value;
 form.requestSubmit(); return true;
})()`, version, source.RepositoryURL, source.CommitSHA, source.ArtifactRef, source.CIRunURL))
		b.wait(
			t,
			fmt.Sprintf(
				`!document.querySelector('.htmx-request,.htmx-settling') && [...document.querySelectorAll('#releases-content tbody tr')].some(r => r.textContent.includes(%q))`,
				version,
			),
		)
		var releases struct{ Items []db.Release }
		decodeStepLogTest(
			t,
			f.api(t, "GET", f.base()+"/releases", nil, 200),
			&releases,
		)
		var release db.Release
		for _, item := range releases.Items {
			if item.Version == version {
				release = item
			}
		}
		if release.ID == 0 {
			t.Fatal("browser release was not persisted")
		}
		b.navigateBackTest(
			t,
			fmt.Sprintf("%s%s/%d", f.baseURL, path, release.ID),
		)
		if string(b.evaluate(t, fmt.Sprintf(`(() => {
 const section=document.querySelector('[data-release-provenance]');
 return section && [%q,%q,%q,%q].every(v => section.textContent.includes(v)) && document.documentElement.scrollWidth <= innerWidth;
})()`, source.RepositoryURL, source.CommitSHA, source.ArtifactRef, source.CIRunURL))) != "true" {
			t.Fatal("source metadata is missing or overflows")
		}
		b.screenshot(t, fmt.Sprintf("provenance-release-%d", width))
		b.navigateBackTest(t, f.baseURL+path)
		b.evaluate(
			t,
			`(() => { const form=document.querySelector('#release-version').form; form.elements.version.value='unsafe'; form.elements.repository_url.value='https://user:browser-secret@example.test/repo'; form.requestSubmit(); return true; })()`,
		)
		b.wait(
			t,
			`document.querySelector('#release-create-error') && !document.querySelector('.htmx-request,.htmx-settling')`,
		)
		if string(
			b.evaluate(
				t,
				`!document.body.textContent.includes('browser-secret') && !document.querySelector('[name=repository_url]').value && document.documentElement.scrollWidth <= innerWidth`,
			),
		) != "true" {
			t.Fatal(
				"rejected provenance leaked credentials or broke form layout",
			)
		}
		b.screenshot(t, fmt.Sprintf("provenance-rejection-%d", width))
	}
}

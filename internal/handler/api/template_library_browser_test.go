//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestTemplateLibraryBrowserE2E(t *testing.T) {
	f := newLibraryE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	b.navigateBackTest(t, f.server.URL+"/templates")
	b.evaluate(
		t,
		`document.querySelector('a[href="/templates?type=library"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-template-library]') && document.querySelector('nav[aria-label="Template types"] a[aria-current="true"]').textContent==='Template Library'`,
	)
	b.screenshot(t, "template-library-list-desktop")
	b.evaluate(
		t,
		`document.querySelector('a[href="/template-library/linux-service"]').click(); true`,
	)
	b.wait(t, `document.querySelector('[data-library-preview]')`)
	b.evaluate(
		t,
		fmt.Sprintf(`document.querySelector('a[href=%q]').click(); true`,
			strings.TrimPrefix(f.path, "/api/v1")+"/linux-service"),
	)
	b.wait(t, `document.querySelector('[data-apply-library-template]')`)
	b.screenshot(t, "template-library-workflow-preview-desktop")
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 390, "height": 844, "deviceScaleFactor": 1, "mobile": true,
	}, &struct{}{})
	b.wait(t, `document.documentElement.scrollWidth<=innerWidth`)
	b.screenshot(t, "template-library-workflow-preview-phone")
	// Bypass HTML validation to exercise the server's required-field feedback.
	b.evaluate(
		t,
		`const f=document.querySelector('[data-apply-library-template]'); f.noValidate=true; f.requestSubmit(); true`,
	)
	b.wait(t, `document.querySelector('[data-library-error]')`)
	b.evaluate(
		t,
		`document.querySelector('#variable-ARTIFACT_URL').value='https://artifacts.example.test/app'; document.querySelector('#variable-ARTIFACT_TOKEN').value='browser-fresh-secret'; document.querySelector('#variable-INSTALL_PATH').value='/opt/browser/app'; document.querySelector('#variable-SERVICE_NAME').value='browser.service'; document.querySelector('#variable-HEALTH_URL').value='http://127.0.0.1:8080/health'; document.querySelector('#library-agent-selectors').value='library-host'; document.querySelector('#library-reviewed').checked=true; document.querySelector('[data-apply-library-template]').requestSubmit(); true`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname==='/projects/%d' && document.querySelector('#project-detail')`,
			f.project.ID,
		),
	)
	b.screenshot(t, "template-library-applied-phone")
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) != 4 {
		t.Fatal("browser apply did not create complete workflow", err)
	}
	// An existing-variable collision must stay on the preview with feedback.
	b.navigateBackTest(
		t,
		f.server.URL+strings.TrimPrefix(f.path, "/api/v1")+"/linux-service",
	)
	b.evaluate(
		t,
		`document.querySelector('#variable-ARTIFACT_URL').value='https://artifacts.example.test/app'; document.querySelector('#variable-ARTIFACT_TOKEN').value='rejected-browser-secret'; document.querySelector('#variable-INSTALL_PATH').value='/opt/browser/app'; document.querySelector('#variable-SERVICE_NAME').value='browser.service'; document.querySelector('#variable-HEALTH_URL').value='http://127.0.0.1:8080/health'; document.querySelector('#library-agent-selectors').value='library-host'; document.querySelector('#library-reviewed').checked=true; document.querySelector('[data-apply-library-template]').requestSubmit(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-library-error]')?.textContent.includes('already exists')`,
	)
	if string(
		b.evaluate(
			t,
			`document.documentElement.outerHTML.includes('rejected-browser-secret') || document.querySelector('#variable-ARTIFACT_TOKEN').value!==''`,
		),
	) != "false" {
		t.Fatal("browser rejection reflected a secret")
	}
	b.screenshot(t, "template-library-conflict-phone")
	f.changeRole(t, "project", "viewer")
	b.navigateBackTest(
		t,
		f.server.URL+strings.TrimPrefix(
			f.path,
			"/api/v1",
		)+"/compose-application",
	)
	b.wait(
		t,
		`document.querySelector('[data-library-readonly]') && !document.querySelector('[data-apply-library-template]')`,
	)
	b.screenshot(t, "template-library-viewer-phone")
}

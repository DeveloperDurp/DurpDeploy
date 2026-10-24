//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func TestTemplateLibraryVariantsBrowserE2E(t *testing.T) {
	for _, slug := range []string{
		"linux-service-powershell", "compose-application-python", "terraform-plan-apply",
	} {
		t.Run(slug, func(t *testing.T) { checkLibraryVariantBrowser(t, slug) })
	}
}

func checkLibraryVariantBrowser(t *testing.T, slug string) {
	t.Helper()
	f := newLibraryE2E(t)
	entry := f.preview(t, slug)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	b.navigateBackTest(
		t,
		f.server.URL+strings.TrimPrefix(f.path, "/api/v1")+"/"+slug,
	)
	b.wait(t, `document.querySelector('[data-apply-library-template]')`)
	b.screenshot(t, "library-"+slug+"-desktop")
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 390, "height": 844, "deviceScaleFactor": 1, "mobile": true,
	}, &struct{}{})
	b.wait(t, `document.documentElement.scrollWidth<=innerWidth`)
	b.screenshot(t, "library-"+slug+"-phone")
	if slug == "terraform-plan-apply" {
		b.wait(
			t,
			`!document.querySelector('#library-agent-selectors') && document.querySelector('[data-library-preview]').textContent.includes('Human approval required')`,
		)
	}
	b.evaluate(
		t,
		`const form=document.querySelector('form[data-apply-library-template]'); form.noValidate=true; form.requestSubmit(); true`,
	)
	b.wait(t, `document.querySelector('[data-library-error]')`)
	input := libraryInput(entry)
	encoded, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	fill := fmt.Sprintf(`const input=%s;
for (const [key,value] of Object.entries(input.variable_values)) {
    document.getElementById('variable-'+key).value=value;
}
const labels=document.querySelector('#library-agent-selectors');
if (labels) labels.value=(input.agent_selectors||[]).join(', ');
document.querySelector('#library-reviewed').checked=true;
document.querySelector('[data-apply-library-template]').requestSubmit(); true`, encoded)
	b.evaluate(t, fill)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname==='/projects/%d' && document.querySelector('#project-detail')`,
			f.project.ID,
		),
	)
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) != len(entry.Snapshot.Steps) {
		t.Fatal("incomplete browser copy", err)
	}
	for index, step := range steps {
		want := entry.Snapshot.Steps[index]
		if step.Interpreter != want.Interpreter ||
			step.ApprovalArtifactPath != want.ApprovalArtifactPath {
			t.Fatal("browser copy changed interpreter or approval boundary")
		}
	}
	b.screenshot(t, "library-"+slug+"-applied")
}

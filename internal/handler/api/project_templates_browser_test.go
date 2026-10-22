//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestProjectTemplatesBrowserE2E(t *testing.T) {
	f := newProjectTemplateE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	projectPath := fmt.Sprintf("/projects/%d", f.project.ID)
	b.navigateBackTest(t, f.server.URL+projectPath)
	if string(
		b.evaluate(
			t,
			`!!document.querySelector('#project-detail .page-header a[href$="/project-templates/new"]')`,
		),
	) != "false" {
		t.Fatal("save template remains in the project header")
	}
	b.evaluate(
		t,
		`document.querySelector('button[aria-controls="project-menu-dialog"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#project-menu-dialog').matches(':modal')`,
	)
	b.screenshot(t, "project-template-save-flyout")
	if string(
		b.evaluate(
			t,
			`document.querySelector('#project-menu-dialog a[href$="/project-templates/new"]').closest('ul').querySelector('.menu-title').textContent`,
		),
	) != `"Settings"` {
		t.Fatal("Save as Template is outside project Settings")
	}
	b.evaluate(
		t,
		`document.querySelector('#project-menu-dialog a[href$="/project-templates/new"]').click(); true`,
	)
	b.wait(t, `document.querySelector('[data-save-project-template]')`)
	b.evaluate(
		t,
		`document.querySelector('#template-name').value='Web reusable project'; document.querySelector('#template-description').value='Saved through the browser'; document.querySelector('[data-save-project-template]').requestSubmit(); true`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname===%q && document.querySelector('#project-detail')`,
			projectPath,
		),
	)
	b.navigateBackTest(t, f.server.URL+"/templates?type=projects")
	b.wait(
		t,
		`document.querySelector('[data-project-templates] a[aria-label="Open Web reusable project"]')`,
	)
	b.evaluate(
		t,
		`document.querySelector('[data-project-templates] a[aria-label="Open Web reusable project"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-apply-project-template]') && document.querySelector('main h1')?.textContent==='Web reusable project'`,
	)
	if string(
		b.evaluate(
			t,
			`document.body.textContent.includes('source-secret-must-be-absent') || document.body.textContent.includes('source-package-must-be-absent')`,
		),
	) != "false" {
		t.Fatal("browser preview exposes source secrets")
	}
	b.screenshot(t, "project-template-preview-desktop")
	b.call(
		t,
		"Emulation.setDeviceMetricsOverride",
		map[string]any{
			"width":             390,
			"height":            844,
			"deviceScaleFactor": 1,
			"mobile":            true,
		},
		&struct{}{},
	)
	b.wait(t, `document.documentElement.scrollWidth <= innerWidth`)
	b.screenshot(t, "project-template-preview-phone")
	var overrideKey string
	for _, variable := range f.snapshot.Variables {
		if variable.Name == "COPY_DEFAULT" {
			overrideKey = variable.Key
		}
	}
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('#override-%s').checked=true; document.querySelector('#variable-%s').value='browser replacement default'; true`,
			overrideKey,
			overrideKey,
		),
	)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('#project-name').value='Web template destination'; for (const select of document.querySelectorAll('select[name^="environment_"]')) select.value=%q; for (const input of document.querySelectorAll('input[name^="variable_"][type="password"]')) input.value='browser-fresh-secret'; document.querySelector('#package-credential').value='browser-fresh-credential'; document.querySelector('#template-reviewed').checked=true; document.querySelector('[data-apply-project-template]').requestSubmit(); true`,
			fmt.Sprint(f.environment.ID),
		),
	)
	b.wait(
		t,
		`/^\/projects\/\d+$/.test(location.pathname) && document.querySelector('main h1')?.textContent==='Web template destination'`,
	)
	var destination string
	decodeStepLogTest(t, b.evaluate(t, `location.pathname`), &destination)
	var steps struct {
		Items []struct {
			Name       string
			ScriptBody string `json:"script_body"`
		}
	}
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "GET", "/api/v1"+destination+"/steps", nil, 200),
		&steps,
	)
	if len(steps.Items) != len(f.snapshot.Steps) ||
		!strings.Contains(
			steps.Items[0].ScriptBody,
			"original-template-script",
		) {
		t.Fatal("browser apply did not copy step configuration")
	}
	var variables struct {
		Items []struct {
			Name  string
			Value string
		}
	}
	decodeStepLogTest(
		t,
		f.jsonRequest(t, "GET", "/api/v1"+destination+"/variables", nil, 200),
		&variables,
	)
	defaults := map[string]string{}
	for _, variable := range variables.Items {
		defaults[variable.Name] = variable.Value
	}
	for name, want := range map[string]string{"MULTILINE_DEFAULT": "line1\nline2", "CRLF_DEFAULT": "line1\r\nline2", "LEADING_DEFAULT": "\nline1\nline2", "COPY_DEFAULT": "browser replacement default"} {
		if defaults[name] != want {
			t.Fatalf(
				"web apply changed %s: got %q want %q",
				name,
				defaults[name],
				want,
			)
		}
	}
	b.screenshot(t, "project-template-created-phone")
	b.navigateBackTest(t, f.server.URL+"/templates")
	b.wait(
		t,
		`document.querySelector('#templates-list') && document.querySelector('nav[aria-label="Template types"] a[aria-current="true"]').textContent==='Step Templates'`,
	)
	b.evaluate(
		t,
		`document.querySelector('a[href="/templates?type=projects"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname==='/templates' && location.search==='?type=projects' && document.querySelector('main h1')?.textContent==='Templates' && document.querySelector('[data-project-templates]')?.textContent.includes('Web reusable project')`,
	)
	b.wait(t, `document.documentElement.scrollWidth <= innerWidth`)
	b.screenshot(t, "project-templates-list-phone")
	if string(
		b.evaluate(
			t,
			`!!document.querySelector('[data-project-templates] .btn')`,
		),
	) != "false" {
		t.Fatal("project template card still has a separate button")
	}
	b.evaluate(
		t,
		`document.querySelector('[data-project-templates] a[aria-label="Open Web reusable project"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('[data-template-preview]') && document.querySelector('main h1')?.textContent==='Web reusable project'`,
	)
	b.evaluate(t, `history.back(); true`)
	b.wait(
		t,
		`location.search==='?type=projects' && document.querySelector('[data-project-templates]')`,
	)
	b.evaluate(t, `history.back(); true`)
	b.wait(
		t,
		`location.pathname==='/templates' && !location.search && document.querySelector('#templates-list')`,
	)
	b.evaluate(t, `history.forward(); true`)
	b.wait(
		t,
		`location.search==='?type=projects' && document.querySelector('[data-project-templates]')`,
	)
	b.call(
		t,
		"Page.navigate",
		map[string]any{"url": f.server.URL + "/project-templates"},
		&struct{}{},
	)
	b.wait(
		t,
		`location.pathname==='/templates' && location.search==='?type=projects' && document.querySelector('[data-project-templates]')`,
	)
	f.changeRole(t, "project", "viewer")
	b.navigateBackTest(t, f.server.URL+strings.TrimPrefix(f.path, "/api/v1"))
	b.wait(
		t,
		`document.querySelector('[data-template-preview]') && !document.querySelector('[data-apply-project-template]')`,
	)
	b.navigateBackTest(t, f.server.URL+projectPath)
	b.wait(
		t,
		`document.querySelector('#project-detail') && !document.querySelector('a[href$="/project-templates/new"]')`,
	)
}

//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestTemplateMenuBrowserE2E(t *testing.T) {
	f := newProjectTemplateE2E(t)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.server.URL, f.session)
	for _, width := range []int{1280, 390} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1,
			"mobile": width == 390,
		}, &struct{}{})
		b.navigateBackTest(t, f.server.URL+"/templates")
		b.wait(t, `document.querySelector('#templates-list')`)
		var positions []float64
		decodeStepLogTest(
			t,
			b.evaluate(
				t,
				`['.page-header', 'nav[aria-label="Template types"]', '#templates-list'].map(s=>document.querySelector(s).getBoundingClientRect().top)`,
			),
			&positions,
		)
		b.evaluate(
			t,
			`document.querySelector('a[href="/templates?type=projects"]').click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('[data-project-templates]') && !document.querySelector('.htmx-settling')`,
		)
		var projectPositions []float64
		decodeStepLogTest(
			t,
			b.evaluate(
				t,
				`['.page-header', 'nav[aria-label="Template types"]', '[data-project-templates]'].map(s=>document.querySelector(s).getBoundingClientRect().top)`,
			),
			&projectPositions,
		)
		for i, position := range positions {
			if position != projectPositions[i] {
				t.Fatalf(
					"layout moved at width %d: %v -> %v",
					width,
					positions,
					projectPositions,
				)
			}
		}
		b.evaluate(
			t,
			`document.querySelector('[data-template-help-trigger]').click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('[data-template-help]').matches(':modal')`,
		)
		b.evaluate(
			t,
			`document.querySelector('[data-template-help] .modal-action button').click(); true`,
		)
		b.wait(t, `!document.querySelector('[data-template-help]').open`)
		b.evaluate(
			t,
			`document.querySelector('[data-template-create-menu] > summary').click(); true`,
		)
		b.wait(t, `document.querySelector('[data-template-create-menu]').open`)
		b.screenshot(t, fmt.Sprintf("template-create-menu-%d", width))
		b.evaluate(
			t,
			`document.querySelector('a[href="/templates/project-source"]').click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('#project-edit-dialog').matches(':modal') && document.querySelector('[data-project-template-sources] [data-resource-card]')`,
		)
		b.screenshot(t, fmt.Sprintf("template-project-sources-%d", width))
		b.evaluate(
			t,
			`document.querySelector('[data-project-template-sources] a[x-data="backNavigation"]').click(); true`,
		)
		b.wait(
			t,
			`!document.querySelector('#project-edit-dialog').open && document.activeElement?.matches('[data-template-create-menu] > summary')`,
		)
		b.wait(t, `document.documentElement.scrollWidth<=innerWidth`)
	}
	b.evaluate(
		t,
		`document.querySelector('[data-template-create-menu] > summary').click(); document.querySelector('a[href="/templates/new"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal') && document.querySelector('#template-settings-form')`,
	)
	b.evaluate(
		t,
		`document.querySelector('#template-settings-form [name="name"]').value='Created from shared menu'; document.querySelector('#template-container-image').value='harbor.durp.info/dockerhub/library/bash:5.2'; document.querySelector('#template-settings-form textarea[name="script_body"]').value='echo shared-menu'; document.querySelector('#template-settings-form').requestSubmit(); true`,
	)
	b.wait(
		t,
		`location.pathname==='/templates' && !location.search && document.querySelector('#templates-list')?.textContent.includes('Created from shared menu') && !document.querySelector('dialog[open]')`,
	)
	f.jsonRequest(t, "GET", "/api/v1/templates", nil, 200)
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	b.evaluate(
		t,
		`document.querySelector('[data-template-create-menu] > summary').click(); document.querySelector('a[href="/templates/project-source"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog')?.matches(':modal') && document.querySelector('[data-project-template-sources] [data-resource-card] a')`,
	)
	b.evaluate(
		t,
		`document.querySelector('[data-project-template-sources] [data-resource-card] a').click(); true`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname==='/projects/%d/project-templates/new' && document.querySelector('[data-save-project-template]') && !document.querySelector('dialog[open]')`,
			f.project.ID,
		),
	)
	b.screenshot(t, "template-source-direct-save")
	b.evaluate(
		t,
		`document.querySelector('#template-name').value='Saved from Templates'; document.querySelector('[data-save-project-template]').requestSubmit(); true`,
	)
	b.wait(
		t,
		`location.pathname==='/templates' && location.search==='?type=projects' && document.querySelector('[data-project-templates]')?.textContent.includes('Saved from Templates')`,
	)
	b.screenshot(t, "template-saved-return-to-templates")
	f.changeRole(t, "global", "viewer")
	b.navigateBackTest(t, f.server.URL+"/templates?type=projects")
	b.wait(
		t,
		`document.querySelector('[data-project-templates]') && !document.querySelector('[data-template-create-menu]')`,
	)
}

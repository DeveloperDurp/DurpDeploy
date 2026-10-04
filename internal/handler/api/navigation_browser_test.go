//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
)

func TestPageNavigationBrowserE2E(t *testing.T) {
	// Given the real application and an authenticated browser.
	f := newArtifactE2E(t)
	release := seedRelease(t, f.h.repo, f.project.ID)
	deployment := seedDeployment(
		t,
		f.h.repo,
		release.ID,
		f.environment.ID,
		"running",
	)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, f.baseURL+"/projects")
	b.wait(
		t,
		`document.querySelector('a[href="/environments"]').getAttribute('hx-boost') === 'true'`,
	)
	b.evaluate(
		t,
		`window.navigationDocument = {}; window.navigationHead = document.head; true`,
	)
	// When following normal page links and restoring browser history.
	b.clickPageNavigation(t, "/environments", "Environments")
	b.captureNavigation(t, "environments")
	b.clickPageNavigation(t, "/templates", "Templates")
	b.captureNavigation(t, "templates")
	b.evaluate(t, `history.back(); true`)
	b.wait(
		t,
		`location.pathname === '/environments' && document.title === 'Environments' && document.querySelector('#app-navbar a[href="/environments"]').classList.contains('active')`,
	)
	b.evaluate(t, `history.forward(); true`)
	b.wait(
		t,
		`location.pathname === '/templates' && document.title === 'Templates' && document.querySelector('#app-navbar a[href="/templates"]').classList.contains('active')`,
	)
	// Then the shell and bundles remain loaded, without persisted HTML history.
	if string(
		b.evaluate(
			t,
			`!!window.navigationDocument && document.head === window.navigationHead && !sessionStorage.getItem('htmx-history-cache') && performance.getEntriesByType('resource').filter(e => /tailwind.min.css|app.bundle.js/.test(e.name)).length === 2`,
		),
	) != "true" {
		t.Fatal("navigation reloaded bundles or saved protected page markup")
	}
	b.clickPageNavigation(t, "/deployments", "Deployments")
	path := fmt.Sprintf("/deployments/%d", deployment.ID)
	b.wait(
		t,
		fmt.Sprintf(
			`document.querySelector('a[href=%q]')?.getAttribute('hx-boost') === 'true'`,
			path,
		),
	)
	b.evaluate(
		t,
		fmt.Sprintf(`document.querySelector('a[href=%q]').click(); true`, path),
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname === %q && window.Alpine && document.querySelector('[x-data^="deploymentStepLogs"]') && Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).source?.readyState === 1`,
			path,
		),
	)
	b.evaluate(
		t,
		`window.previousLogSource = Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).source; true`,
	)
	if string(
		b.evaluate(
			t,
			`!document.querySelector('a[href$="/logs.txt"]').hasAttribute('hx-boost')`,
		),
	) != "true" {
		t.Fatal("log download was boosted")
	}
	b.clickPageNavigation(t, "/environments", "Environments")
	b.wait(t, `window.previousLogSource.readyState === 2`)
	b.clickPageNavigation(t, "/environments/new", "Environment")
	if string(
		b.evaluate(
			t,
			`!document.querySelector('main form').hasAttribute('hx-boost') && !document.querySelector('form[action="/logout"]').hasAttribute('hx-boost')`,
		),
	) != "true" {
		t.Fatal("page navigation changed form behavior")
	}
	b.evaluate(
		t,
		`document.querySelector('main input[name="name"]').value = 'navigation-created'; document.querySelector('main form').requestSubmit(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/environments' && document.readyState === 'complete' && document.querySelector('main').textContent.includes('navigation-created') && !window.navigationDocument`,
	)
	// An ordinary full page load reuses the cached CSS and JS.
	b.wait(
		t,
		`performance.getEntriesByType('resource').filter(e => /tailwind.min.css|app.bundle.js/.test(e.name)).length === 2`,
	)
	if string(
		b.evaluate(
			t,
			`performance.getEntriesByType('resource').filter(e => /tailwind.min.css|app.bundle.js/.test(e.name)).every(e => e.transferSize === 0)`,
		),
	) != "true" {
		t.Fatal("full navigation downloaded cached bundles again")
	}
	// Direct links also remain usable when application JavaScript is disabled.
	b.call(
		t,
		"Emulation.setScriptExecutionDisabled",
		map[string]bool{"value": true},
		&struct{}{},
	)
	b.call(
		t,
		"Page.navigate",
		map[string]string{"url": f.baseURL + "/projects"},
		&struct{}{},
	)
	b.wait(
		t,
		`location.pathname === '/projects' && document.readyState === 'complete' && document.querySelector('h1') && !window.Alpine`,
	)
	b.evaluate(
		t,
		`document.querySelector('a[href="/environments"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/environments' && document.readyState === 'complete' && document.querySelector('h1').textContent === 'Environments' && !window.Alpine`,
	)
}

func TestPageNavigationExpiredSessionBrowserE2E(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprintf("history=%t", history), func(t *testing.T) {
			f := newArtifactE2E(t)
			b := startPackageBrowser(t)
			b.setBackTestSession(t, f.baseURL, f.session)
			b.navigateBackTest(t, f.baseURL+"/projects")
			b.wait(
				t,
				`document.querySelector('a[href="/environments"]').getAttribute('hx-boost') === 'true'`,
			)
			b.evaluate(t, `window.navigationDocument = {}; true`)
			if history {
				b.clickPageNavigation(t, "/environments", "Environments")
			}
			if _, err := f.h.repo.DB.ExecContext(
				t.Context(),
				"UPDATE sessions SET expires_at=0 WHERE id=?",
				f.session,
			); err != nil {
				t.Fatal(err)
			}
			if history {
				b.evaluate(t, `history.back(); true`)
			} else {
				b.evaluate(
					t,
					`document.querySelector('a[href="/environments"]').click(); true`,
				)
			}
			b.wait(
				t,
				`location.pathname === '/login' && document.readyState === 'complete' && document.querySelector('input[name="password"]') && !window.navigationDocument && !document.querySelector('meta[name="csrf-token"]')`,
			)
		})
	}
}

func (b *packageBrowser) clickPageNavigation(t *testing.T, path, title string) {
	t.Helper()
	b.wait(
		t,
		fmt.Sprintf(
			`document.querySelector('a[href=%q]')?.getAttribute('hx-boost') === 'true'`,
			path,
		),
	)
	b.evaluate(
		t,
		fmt.Sprintf(`document.querySelector('a[href=%q]').click(); true`, path),
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname === %q && document.title === %q && document.activeElement?.id === 'page-content'`,
			path,
			title,
		),
	)
}

func (b *packageBrowser) captureNavigation(t *testing.T, name string) {
	t.Helper()
	for _, theme := range []string{"mocha", "light"} {
		b.evaluate(
			t,
			fmt.Sprintf(
				`Alpine.$data(document.getElementById('app-navbar')).theme = %q; true`,
				theme,
			),
		)
		b.wait(
			t,
			fmt.Sprintf(`document.documentElement.dataset.theme === %q`, theme),
		)
		for _, width := range []int{375, 768, 1280} {
			b.call(
				t,
				"Emulation.setDeviceMetricsOverride",
				map[string]any{
					"width":             width,
					"height":            900,
					"deviceScaleFactor": 1,
					"mobile":            false,
				},
				&struct{}{},
			)
			if string(
				b.evaluate(
					t,
					`document.documentElement.scrollWidth <= innerWidth`,
				),
			) != "true" {
				b.screenshot(
					t,
					"overflow-"+name+"-"+theme+"-"+fmt.Sprint(width),
				)
				t.Fatal("navigation overflows viewport")
			}
			b.wait(
				t,
				`!document.querySelector('.htmx-settling') && document.getAnimations().every(a => a.playState !== 'running')`,
			)
			b.screenshot(
				t,
				fmt.Sprintf("navigation-%s-%s-%d", name, theme, width),
			)
		}
	}
}

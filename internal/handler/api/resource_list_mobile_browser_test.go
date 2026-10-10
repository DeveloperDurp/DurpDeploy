//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestResourceListMobileBrowserE2E(t *testing.T) {
	// Given: API-created lists with long values and a multi-stage project.
	f := newArtifactE2E(t)
	var lifecycle struct{ ID int64 }
	description := strings.Repeat("description-without-spaces-", 8)
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{
			"name":        strings.Repeat("mobile-lifecycle-", 5),
			"description": description,
		}, 201), &lifecycle)
	var project struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/projects",
		map[string]any{
			"name":         strings.Repeat("mobile-project-", 5),
			"description":  description,
			"lifecycle_id": lifecycle.ID,
		}, 201), &project)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST",
		fmt.Sprintf("/api/v1/projects/%d/releases", project.ID),
		map[string]string{"version": strings.Repeat("release-", 10)},
		201), &release)
	for i := range 4 {
		var environment struct{ ID int64 }
		decodeStepLogTest(t, f.api(t, "POST", "/api/v1/environments",
			map[string]string{
				"name": fmt.Sprintf(
					"%d-%s",
					i,
					strings.Repeat("environment-", 5),
				),
				"description": description,
				"tags":        strings.Repeat("long-tag-", 8),
			}, 201), &environment)
		f.api(t, "POST",
			fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
			map[string]int64{"environment_id": environment.ID}, 201)
		if i < 3 {
			seedDeployment(t, f.h.repo, release.ID, environment.ID, "succeeded")
		}
	}
	viewer := seedAPIUser(t, f.h.repo, "list-viewer@example.test", "viewer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: project.ID, UserID: viewer.ID, Role: "deployer",
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{
			ID: "resource-list-viewer", UserID: viewer.ID,
			CsrfToken: f.csrf, ExpiresAt: 4102444800,
		}); err != nil {
		t.Fatal(err)
	}
	b := startPackageBrowser(t)
	for _, session := range []string{f.session, "resource-list-viewer"} {
		captureResourceLists(t, f, b, session, project.ID, lifecycle.ID)
	}
	for _, path := range []string{
		fmt.Sprintf("/environments/%d/edit", f.environment.ID),
		fmt.Sprintf("/lifecycles/%d", lifecycle.ID),
	} {
		b.navigateBackTest(t, f.baseURL+path)
		if string(b.evaluate(
			t,
			`!document.querySelector('main button[hx-delete], main input[name="_method"][value="delete"]')`,
		)) != "true" {
			t.Fatal("viewer edit page exposes Delete")
		}
	}
	// And: the project link still opens its detail through HTMX.
	b.setBackTestSession(t, f.baseURL, f.session)
	for _, path := range []string{"/environments/new", "/lifecycles/new"} {
		b.navigateBackTest(t, f.baseURL+path)
		if string(b.evaluate(
			t,
			`!document.querySelector('main button[hx-delete], main input[name="_method"][value="delete"]')`,
		)) != "true" {
			t.Fatal("new form exposes Delete")
		}
	}
	b.navigateBackTest(t, f.baseURL+"/projects")
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('main a[href="/projects/%d"]').click(); true`,
		project.ID))
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname === '/projects/%d' && document.activeElement?.id === 'page-content'`,
			project.ID,
		),
	)
	// And: row actions retain their navigation and fragment behavior.
	historyPath := fmt.Sprintf("/deployments?project_id=%d", project.ID)
	var history struct{ Total int64 }
	decodeStepLogTest(t, f.api(t, "GET", "/api/v1"+historyPath,
		nil, 200), &history)
	if history.Total != 3 {
		t.Fatal("project deployment API filter lost its history")
	}
	for _, session := range []string{f.session, "resource-list-viewer"} {
		b.setBackTestSession(t, f.baseURL, session)
		b.navigateBackTest(t, fmt.Sprintf("%s/projects/%d", f.baseURL,
			project.ID))
		if session == "resource-list-viewer" && string(b.evaluate(
			t,
			`!document.querySelector('main .page-header a[href$="/deploy"], #project-menu-dialog a[href$="/schedules"]')`,
		)) != "true" {
			t.Fatal("viewer project menu exposes write controls")
		}
		b.captureNavigation(t, "project-menu-overview")
		if string(b.evaluate(t, `(() => {
 document.querySelector('button[aria-controls="project-menu-dialog"]').click();
 const panel = document.querySelector('.project-menu-panel');
 const animation = panel.getAnimations().find(a => a.animationName === 'project-menu-enter');
 if (!animation) return false;
 animation.pause();
 animation.currentTime = 0;
 const start = panel.getBoundingClientRect().left;
 animation.currentTime = 100;
 const middle = panel.getBoundingClientRect().left;
 animation.currentTime = 200;
 const end = panel.getBoundingClientRect().left;
 animation.play();
 return start >= innerWidth - 16 && end < middle && middle < start;
})()`)) != "true" {
			t.Fatal("project drawer does not enter from the right")
		}
		b.captureNavigation(t, "project-deployment-history", func() {
			b.wait(t, fmt.Sprintf(`(() => {
 const link = document.querySelector('nav[aria-label="Project sections"] a[href=%q]');
 if (!link || link.textContent !== 'Deployment history') return false;
 const rect = link.getBoundingClientRect();
 const dialog = link.closest('dialog');
 const panel = dialog.querySelector('.modal-box');
 const box = panel.getBoundingClientRect();
 const navbarBottom = Math.max(0, document.querySelector('#app-navbar').getBoundingClientRect().bottom);
 const close = panel.querySelector('button').getBoundingClientRect();
 return dialog.matches(':modal') && Math.abs(box.right - innerWidth) <= 16 &&
 Math.abs(box.top - navbarBottom) <= 1 && Math.abs(box.bottom - innerHeight) <= 1 &&
 panel.scrollWidth <= panel.clientWidth && box.left >= 0 &&
 close.top >= 0 && close.bottom <= innerHeight &&
 ![...dialog.querySelectorAll('nav a')].some(a => a.classList.contains('btn')) &&
 rect.height >= 44 && rect.right <= innerWidth && rect.left >= 0;
})()`, historyPath))
		})
		// Closing by Escape, backdrop, and Close keeps the page and restores focus.
		b.call(t, "Input.dispatchKeyEvent", map[string]any{
			"type": "keyDown", "key": "Escape", "code": "Escape",
			"windowsVirtualKeyCode": 27,
		}, &struct{}{})
		b.wait(t, `!document.querySelector('#project-menu-dialog').open`)
		b.evaluate(
			t,
			`document.querySelector('button[aria-controls="project-menu-dialog"]').focus(); document.activeElement.click(); true`,
		)
		b.wait(
			t,
			`document.querySelector('#project-menu-dialog').matches(':modal')`,
		)
		for _, kind := range []string{"mousePressed", "mouseReleased"} {
			b.call(t, "Input.dispatchMouseEvent", map[string]any{
				"type":       kind,
				"x":          1,
				"y":          100,
				"button":     "left",
				"clickCount": 1,
			}, &struct{}{})
		}
		b.wait(
			t,
			`!document.querySelector('#project-menu-dialog').open && document.activeElement?.getAttribute('aria-controls') === 'project-menu-dialog'`,
		)
		b.call(t, "Emulation.setEmulatedMedia", map[string]any{
			"features": []map[string]string{
				{"name": "prefers-reduced-motion", "value": "reduce"},
			},
		}, &struct{}{})
		if string(b.evaluate(t, `(() => {
 document.activeElement.click();
 const dialog = document.querySelector('#project-menu-dialog');
 const still = getComputedStyle(dialog.querySelector('.project-menu-panel')).animationName === 'none';
 dialog.querySelector('button[aria-label="Close project menu"]').click();
 return still && !dialog.open;
})()`)) != "true" {
			t.Fatal("reduced-motion project drawer still slides")
		}
		b.call(t, "Emulation.setEmulatedMedia", map[string]any{
			"features": []map[string]string{},
		}, &struct{}{})
		b.evaluate(t, `document.activeElement.click(); true`)
		b.wait(
			t,
			`document.querySelector('#project-menu-dialog').matches(':modal')`,
		)
		b.evaluate(
			t,
			`document.querySelector('#project-menu-dialog button[aria-label="Close project menu"]').click(); true`,
		)
		b.wait(
			t,
			`!document.querySelector('#project-menu-dialog').open && document.activeElement?.getAttribute('aria-controls') === 'project-menu-dialog'`,
		)
		b.evaluate(t, `document.activeElement.click(); true`)
		b.wait(
			t,
			`document.querySelector('#project-menu-dialog').matches(':modal')`,
		)
		b.evaluate(t, fmt.Sprintf(
			`window.projectHistoryNavigation = true; document.querySelector('nav[aria-label="Project sections"] a[href=%q]').focus(); true`,
			historyPath,
		))
		for _, kind := range []string{"keyDown", "keyUp"} {
			b.call(t, "Input.dispatchKeyEvent", map[string]any{
				"type": kind, "key": "Enter", "code": "Enter",
				"windowsVirtualKeyCode": 13,
			}, &struct{}{})
		}
		b.wait(t, fmt.Sprintf(`location.pathname === '/deployments' &&
 new URLSearchParams(location.search).get('project_id') === '%d' &&
 document.querySelector('select[name="project_id"]')?.value === '%d' &&
 document.querySelectorAll('#deployments-tbody [data-deployment-id]').length === 3 &&
 !document.querySelector('#project-menu-dialog') &&
 window.projectHistoryNavigation === true`, project.ID, project.ID))
	}
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, f.baseURL+"/environments")
	edit := fmt.Sprintf("/environments/%d/edit", f.environment.ID)
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('main a[href=%q]').click(); true`, edit))
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal') && document.querySelector('main button[hx-delete]') !== null`,
	)
	b.captureNavigation(t, "mobile-environment-edit", func() {
		assertEditDeleteControl(t, b, `main button[hx-delete]`)
	})
	b.evaluate(
		t,
		`window.environmentFormBeforeSave = document.querySelector('form[hx-put]'); document.querySelector('input[name="name"]').value = 'saved-phone-environment'; document.querySelector('button[form="environment-settings-form"]').click(); true`,
	)
	b.wait(
		t,
		`!document.querySelector('#project-edit-dialog').open && document.querySelector('main').textContent.includes('saved-phone-environment')`,
	)
	var saved struct{ Name string }
	decodeStepLogTest(
		t,
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
			nil,
			200,
		),
		&saved,
	)
	if saved.Name != "saved-phone-environment" {
		t.Fatal("Save did not persist environment settings")
	}
	b.wait(t, `!document.querySelector('.htmx-settling, .htmx-request')`)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('main a[href=%q]').click(); true`,
			edit,
		),
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal')`,
	)
	b.captureNavigation(t, "mobile-environment-saved", func() {
		assertEditDeleteControl(t, b, `main button[hx-delete]`)
	})
	b.evaluate(
		t,
		`document.querySelector('main a[x-data="backNavigation"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/environments' && !document.querySelector('#project-edit-dialog').open && !document.querySelector('#project-edit-content').childElementCount && !document.querySelector('.htmx-settling, .htmx-request')`,
	)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('main a[href=%q]').click(); true`,
			edit,
		),
	)
	b.wait(
		t,
		`document.querySelector('#project-edit-dialog').matches(':modal') && document.querySelector('main button[hx-delete]') !== null`,
	)
	b.evaluate(
		t,
		`window.confirm = () => false; document.querySelector('main button[hx-delete]').click(); true`,
	)
	f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
		nil,
		200,
	)
	b.evaluate(t, fmt.Sprintf(
		`window.confirm = () => true; document.querySelector('button[hx-delete="/environments/%d"]').click(); true`,
		f.environment.ID,
	))
	b.wait(t, fmt.Sprintf(
		`location.pathname === '/environments' && !document.querySelector('button[hx-delete="/environments/%d"]')`,
		f.environment.ID,
	))
	f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
		nil,
		404,
	)
	b.captureNavigation(t, "mobile-environments-deleted", func() {
		assertResourceListMobile(t, b)
	})
	b.navigateBackTest(t, f.baseURL+"/lifecycles")
	lifecyclePath := fmt.Sprintf("/lifecycles/%d", lifecycle.ID)
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('main a[href=%q]').click(); true`,
		lifecyclePath,
	))
	b.wait(
		t,
		`!document.querySelector('dialog[open]') && !!document.querySelector('#shared-variables') && document.querySelector('main form[hx-confirm^="Delete this lifecycle"]') !== null`,
	)
	b.captureNavigation(t, "mobile-lifecycle-edit", func() {
		assertEditDeleteControl(
			t,
			b,
			`main form[hx-confirm^="Delete this lifecycle"] button`,
		)
	})
	b.evaluate(
		t,
		`window.confirm = () => false; document.querySelector('main form[hx-confirm^="Delete this lifecycle"] button').click(); true`,
	)
	f.api(t, "GET", "/api/v1"+lifecyclePath, nil, 200)
	b.evaluate(
		t,
		`window.confirm = () => true; document.querySelector('main form[hx-confirm^="Delete this lifecycle"] button').click(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/lifecycles' && document.readyState === 'complete' && window.Alpine && document.querySelector('main h1')?.textContent === 'Lifecycles' && !document.querySelector('main table')`,
	)
	f.api(t, "GET", "/api/v1"+lifecyclePath, nil, 404)
	b.captureNavigation(t, "mobile-lifecycles-empty")
}

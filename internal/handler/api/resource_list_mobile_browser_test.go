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
	b.navigateBackTest(t, f.baseURL+"/environments")
	edit := fmt.Sprintf("/environments/%d/edit", f.environment.ID)
	b.evaluate(t, fmt.Sprintf(
		`document.querySelector('main a[href=%q]').click(); true`, edit))
	b.wait(t, `document.querySelector('main button[hx-delete]') !== null`)
	b.captureNavigation(t, "mobile-environment-edit", func() {
		assertEditDeleteControl(t, b, `main button[hx-delete]`)
	})
	b.evaluate(
		t,
		`window.environmentFormBeforeSave = document.querySelector('form[hx-put]'); document.querySelector('input[name="name"]').value = 'saved-phone-environment'; document.querySelector('button[form="environment-settings-form"]').click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('form[hx-put]') !== window.environmentFormBeforeSave && document.querySelector('input[name="name"]')?.value === 'saved-phone-environment'`,
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
	b.captureNavigation(t, "mobile-environment-saved", func() {
		assertEditDeleteControl(t, b, `main button[hx-delete]`)
	})
	b.evaluate(
		t,
		`document.querySelector('main a[x-data="backNavigation"]').click(); true`,
	)
	b.wait(
		t,
		`location.pathname === '/environments' && document.querySelector('main h1')?.textContent === 'Environments'`,
	)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('main a[href=%q]').click(); true`,
			edit,
		),
	)
	b.wait(t, `document.querySelector('main button[hx-delete]') !== null`)
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
		`document.querySelector('main form[hx-confirm^="Delete this lifecycle"]') !== null`,
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

//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func (b *packageBrowser) assertLifecyclePermissions(
	t *testing.T,
	f *artifactE2E,
	lifecycleID int64,
) {
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/edit", f.baseURL, f.project.ID),
	)
	b.captureNavigation(t, "project-assignment-admin")
	// Viewer inherits the values but cannot see either write affordance.
	user, err := f.h.repo.Queries.GetUserByEmail(
		t.Context(),
		"artifact-e2e@example.com",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID,
			UserID:    user.ID,
			Role:      "admin",
		},
	); err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.UpdateUser(t.Context(),
		db.UpdateUserParams{ID: user.ID, Name: user.Name, Role: "deployer"},
	); err != nil {
		t.Fatal(err)
	}
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/edit", f.baseURL, f.project.ID),
	)
	if string(
		b.evaluate(
			t,
			`[...document.querySelectorAll('select[name=lifecycle_id] option')].filter(o=>o.value !== '').length === 1`,
		),
	) != "true" {
		t.Fatal("nonadmin can assign a lifecycle")
	}
	b.captureNavigation(t, "project-assignment-readonly")
	b.evaluate(
		t,
		`document.querySelector('select[name=lifecycle_id]').value=''; document.querySelector('[data-project-form] form').requestSubmit(); true`,
	)
	b.wait(
		t,
		fmt.Sprintf(
			`location.pathname === '/projects/%d' && !document.querySelector('[data-inherited-variables]')`,
			f.project.ID,
		),
	)
	b.captureNavigation(t, "project-assignment-removed")
	b.navigateBackTest(t, f.baseURL+"/projects/new")
	b.captureNavigation(t, "project-assignment-new-readonly")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/lifecycles/%d", f.baseURL, lifecycleID),
	)
	if string(
		b.evaluate(
			t,
			`!document.querySelector('input[name=_method][value=delete]') && !document.querySelector('[data-lifecycle-stage-action]') && !document.querySelector('[data-lifecycle-environment-assignment]')`,
		),
	) != "true" {
		t.Fatal("deployer has lifecycle delete or stage write controls")
	}
	b.captureNavigation(t, "lifecycle-deployer")
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/environments/%d/edit", f.baseURL, f.environment.ID),
	)
	if string(
		b.evaluate(t, `!document.querySelector('button[hx-delete]')`),
	) != "true" {
		t.Fatal("deployer has environment delete control")
	}
	b.captureNavigation(t, "environment-deployer")
	if err := f.h.repo.Queries.UpdateUser(
		t.Context(),
		db.UpdateUserParams{ID: user.ID, Name: user.Name, Role: "admin"},
	); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lifecycleID},
		200,
	)
	if err := f.h.repo.Queries.UpdateUser(
		t.Context(),
		db.UpdateUserParams{ID: user.ID, Name: user.Name, Role: "viewer"},
	); err != nil {
		t.Fatal(err)
	}
	b.navigateBackTest(
		t,
		fmt.Sprintf("%s/projects/%d/variables", f.baseURL, f.project.ID),
	)
	b.wait(t, `!!document.querySelector('[data-inherited-variables]')`)
	if string(
		b.evaluate(
			t,
			`document.querySelectorAll('[data-inherited-variables] button').length === 0 && !document.querySelector('[data-inherited-variables] a')`,
		),
	) != "true" {
		t.Fatal("viewer has lifecycle writes")
	}
	b.captureNavigation(t, "project-viewer")
}

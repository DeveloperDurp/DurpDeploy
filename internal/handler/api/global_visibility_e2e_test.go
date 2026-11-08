//go:build e2e

package api_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestGlobalDirectoryEnvironmentVisibilityAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	owner := f.token
	directoryUser := seedAPIUser(t, f.h.repo,
		"directory-only@example.test", "deployer")
	manager := seedAPIUser(t, f.h.repo,
		"directory-manager@example.test", "maintainer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{ProjectID: f.project.ID,
			UserID: manager.ID, Role: "admin"},
	); err != nil {
		t.Fatal(err)
	}
	edit := fmt.Sprintf("/projects/%d/edit", f.project.ID)
	for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
		t.Run(role, func(t *testing.T) {
			assertGlobalVisibilityRole(t, f, role,
				directoryUser.Email, manager.Email)
		})
	}
	f.session = memberRoleSession(t, f.h, manager.ID)
	_, f.token = seedAPIToken(t, f.h.repo, manager.ID)
	page := f.web(t, "GET", edit, nil, 200)
	if !strings.Contains(
		page,
		directoryUser.Name+" ("+directoryUser.Email+")",
	) ||
		!strings.Contains(page, `name="user_id"`) {
		t.Fatal("authorized project administrator cannot see global picker")
	}
	f.api(t, "GET", "/api/v1/admin/users", nil, 403)
	f.token = owner
	scoped := mintProjectToken(t, f, "viewer", f.project.ID)
	f.token = scoped.ID
	body := f.api(t, "GET", "/api/v1/environments", nil, 200)
	if !strings.Contains(string(body), f.environment.Name) {
		t.Fatal("project without a lifecycle lost eligible environments")
	}
	lifecycle, err := f.h.repo.Queries.CreateLifecycle(t.Context(),
		db.CreateLifecycleParams{Name: "visibility-constrained"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.SetProjectLifecycle(t.Context(), db.SetProjectLifecycleParams{
		ID:          f.project.ID,
		LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	// No stages or execution history make this environment eligible.
	body = f.api(t, "GET", "/api/v1/environments", nil, 200)
	if strings.Contains(string(body), f.environment.Name) {
		t.Fatal("project-scoped token gained global environment visibility")
	}
	f.api(t, "GET", "/api/v1/admin/users", nil, 403)
}

func assertGlobalVisibilityRole(
	t *testing.T, f *artifactE2E, role, directoryEmail, managerEmail string,
) {
	t.Helper()
	user := seedAPIUser(t, f.h.repo, role+"@visibility.test", role)
	f.session = memberRoleSession(t, f.h, user.ID)
	_, f.token = seedAPIToken(t, f.h.repo, user.ID)
	// No project membership is required for global environment names.
	page := f.web(t, "GET", "/environments", nil, 200)
	if !strings.Contains(page, f.environment.Name) {
		t.Fatal("non-member cannot read shared environment name")
	}
	body := f.api(t, "GET", "/api/v1/environments", nil, 200)
	if !strings.Contains(string(body), f.environment.Name) {
		t.Fatal("global token cannot read shared environment name")
	}
	status := 403
	if role == "admin" {
		status = 200
	}
	f.web(t, "GET", "/admin/users", nil, status)
	f.api(t, "GET", "/api/v1/admin/users", nil, status)
	edit := fmt.Sprintf("/projects/%d/edit", f.project.ID)
	page = f.web(t, "GET", edit, nil, status)
	if role == "admin" {
		if !strings.Contains(page, directoryEmail) {
			t.Fatal("global administrator cannot see global picker")
		}
		return
	}
	// Read-only membership reveals the roster, without the global picker.
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{ProjectID: f.project.ID,
			UserID: user.ID, Role: "viewer"},
	); err != nil {
		t.Fatal(err)
	}
	page = f.web(t, "GET", edit, nil, 200)
	if !strings.Contains(page, managerEmail) ||
		strings.Contains(page, directoryEmail) ||
		strings.Contains(page, `name="user_id"`) {
		t.Fatal("project roster and global picker gates changed")
	}
}

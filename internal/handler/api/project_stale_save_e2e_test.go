package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
)

type staleProjectSaveFixture struct {
	permissionFixture
	project                    db.Project
	user                       *db.User
	adminToken, token, session string
}

func newStaleProjectSaveFixture(t *testing.T) staleProjectSaveFixture {
	t.Helper()
	f := staleProjectSaveFixture{permissionFixture: newPermissionFixture(t)}
	f.project = seedProject(t, f.h.repo)
	f.user = seedAPIUser(t, f.h.repo, "stale-save@example.test", "maintainer")
	admin := seedAPIUser(t, f.h.repo, "stale-admin@example.test", "admin")
	_, f.adminToken = seedAPIToken(t, f.h.repo, admin.ID)
	_, f.token = seedAPIToken(t, f.h.repo, f.user.ID)
	f.session = memberRoleSession(t, f.h, f.user.ID)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: f.user.ID, Role: "maintainer",
		}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f staleProjectSaveFixture) changeRole(
	t *testing.T, scope, role string,
) {
	t.Helper()
	path := fmt.Sprintf("/api/v1/projects/%d/members/%d",
		f.project.ID, f.user.ID)
	fields := map[string]string{"role": role}
	if scope == "global" {
		path = fmt.Sprintf("/api/v1/admin/users/%d", f.user.ID)
		fields["name"] = f.user.Name
	}
	body, err := json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "PUT", path, string(body), f.adminToken, "", false, 200)
}

func (f staleProjectSaveFixture) assertDescription(
	t *testing.T, want string,
) {
	t.Helper()
	path := fmt.Sprintf("/api/v1/projects/%d", f.project.ID)
	body := f.request(t, "GET", path, "", f.adminToken, "", false, 200)
	var project struct{ Description string }
	if err := json.Unmarshal(body, &project); err != nil {
		t.Fatal(err)
	}
	if project.Description != want {
		t.Fatalf("saved description = %q, want %q", project.Description, want)
	}
}

func TestProjectStaleSaveAPIWebE2E(t *testing.T) {
	for _, scope := range []string{"project", "global"} {
		for _, role := range []string{"viewer", "deployer"} {
			t.Run(scope+"/"+role, func(t *testing.T) {
				f := newStaleProjectSaveFixture(t)
				path := fmt.Sprintf("/projects/%d", f.project.ID)
				f.request(t, "GET", path+"/edit", "", "",
					f.session, false, 200)
				f.changeRole(t, scope, role)
				body, err := json.Marshal(map[string]string{
					"name": f.project.Name, "description": "rejected API edit",
				})
				if err != nil {
					t.Fatal(err)
				}
				f.request(t, "PUT", "/api/v1"+path, string(body),
					f.token, "", false, 403)
				form := url.Values{
					"name": {
						f.project.Name,
					}, "description": {"rejected web edit"},
				}.Encode()
				f.request(t, "PUT", path, form, "", f.session, false, 403)
				f.request(t, "PUT", path, form, "", f.session, true, 200)
				f.assertDescription(t, f.project.Description.String)

				f.changeRole(t, scope, "maintainer")
				f.request(t, "PUT", "/api/v1"+path, string(body),
					f.token, "", false, 200)
				f.assertDescription(t, "rejected API edit")
			})
		}
	}
}

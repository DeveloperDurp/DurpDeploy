//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/roles"
)

type permissionGuidanceFixture struct {
	permissionFixture
	project    db.Project
	adminToken string
}

type permissionGuidanceActor struct {
	user           *db.User
	member         string
	token, session string
}

func newPermissionGuidanceFixture(t *testing.T) permissionGuidanceFixture {
	t.Helper()
	f := permissionGuidanceFixture{permissionFixture: newPermissionFixture(t)}
	f.project = seedProject(t, f.h.repo)
	admin := seedAPIUser(t, f.h.repo, "guidance-admin@example.test", "admin")
	_, f.adminToken = seedAPIToken(t, f.h.repo, admin.ID)
	f.request(
		t,
		"POST",
		f.apiPath("/steps"),
		`{"name":"Guidance step","script_body":"echo guidance","container_image":"harbor.durp.info/dockerhub/library/bash:5.2"}`,
		f.adminToken,
		"",
		false,
		201,
	)
	f.request(t, "POST", f.apiPath("/releases"), `{"version":"guidance-v1"}`,
		f.adminToken, "", false, 201)
	return f
}

func (f permissionGuidanceFixture) apiPath(suffix string) string {
	return fmt.Sprintf("/api/v1/projects/%d%s", f.project.ID, suffix)
}

func (f permissionGuidanceFixture) actor(
	t *testing.T,
	global, member string,
) permissionGuidanceActor {
	t.Helper()
	user := seedAPIUser(t, f.h.repo, global+"-"+member+"@guidance.test", global)
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	if member != "" {
		body := fmt.Sprintf(`{"user_id":%d,"role":%q}`, user.ID, member)
		f.request(t, "POST", f.apiPath("/members"), body,
			f.adminToken, "", false, 201)
	}
	return permissionGuidanceActor{
		user: user, member: member, token: token,
		session: memberRoleSession(t, f.h, user.ID),
	}
}

func TestPermissionGuidanceAPIWebE2E(t *testing.T) {
	f := newPermissionGuidanceFixture(t)
	for _, global := range roles.All() {
		for _, member := range roles.All() {
			t.Run(global+"/"+member, func(t *testing.T) {
				f.checkGuidanceActor(t, f.actor(t, global, member))
			})
		}
	}
	f.checkGuidanceActor(t, f.actor(t, "admin", ""))
}

func (f permissionGuidanceFixture) checkGuidanceActor(
	t *testing.T,
	actor permissionGuidanceActor,
) {
	t.Helper()
	var me struct{ Role string }
	decodeStepLogTest(t, f.request(t, "GET", "/api/v1/users/me", "",
		actor.token, "", false, 200), &me)
	if me.Role != actor.user.Role {
		t.Fatal("current-user API lost the global role")
	}
	var members struct{ Items []db.ListProjectMembersRow }
	decodeStepLogTest(t, f.request(t, "GET", f.apiPath("/members"), "",
		actor.token, "", false, 200), &members)
	if actor.member != "" {
		found := false
		for _, member := range members.Items {
			found = found || member.UserID == actor.user.ID &&
				member.Role == actor.member
		}
		if !found {
			t.Fatal("member API lost the project role")
		}
	}
	effective := me.Role
	if me.Role != "admin" && roles.Rank(actor.member) < roles.Rank(effective) {
		effective = actor.member
	}
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	page := f.request(t, "GET", path, "", "", actor.session, false, 200)
	document := dashboardDocument(t, string(page))
	summary := dashboardElements(document, "data-project-permissions", "")
	if len(summary) != 1 || !strings.Contains(dashboardText(summary[0]),
		"Effective project role: "+effective) {
		t.Fatal("project page does not explain effective permissions")
	}
	if roles.AtLeast(effective, "maintainer") {
		return
	}
	f.checkReadOnlyGuidance(t, actor, path, me.Role)
	before := f.request(t, "GET", f.apiPath(""), "",
		f.adminToken, "", false, 200)
	f.request(t, "PUT", f.apiPath(""),
		`{"name":"unauthorized guidance edit"}`, actor.token, "", false, 403)
	after := f.request(t, "GET", f.apiPath(""), "",
		f.adminToken, "", false, 200)
	if string(before) != string(after) {
		t.Fatal("permission guidance enabled an unauthorized write")
	}
}

func (f permissionGuidanceFixture) checkReadOnlyGuidance(
	t *testing.T,
	actor permissionGuidanceActor,
	path, global string,
) {
	t.Helper()
	for _, suffix := range []string{"/steps-page", "/releases", "/edit"} {
		page := string(f.request(t, "GET", path+suffix, "", "",
			actor.session, false, 200))
		document := dashboardDocument(t, page)
		globalHelp := dashboardElements(document, "href", "/admin/users")
		projectHelp := dashboardElements(
			document,
			"href",
			path+"/edit#project-members",
		)
		if (len(globalHelp) > 0) != !roles.AtLeast(global, "maintainer") ||
			(len(projectHelp) > 0) != !roles.AtLeast(
				actor.member,
				"maintainer",
			) {
			t.Fatalf("incorrect recovery scope for %s/%s", global, actor.member)
		}
		if suffix == "/edit" {
			if !strings.Contains(page, `id="project-members"`) ||
				strings.Contains(page, `data-project-form`) ||
				strings.Contains(page, `id="member-role-`) {
				t.Fatal(
					"recovery page lost read-only members or exposed editing",
				)
			}
		} else if !strings.Contains(page, "Read-only") {
			t.Fatal("unavailable authoring actions have no explanation")
		}
	}
	f.checkGlobalGuidance(t, actor, path, global)
}

func (f permissionGuidanceFixture) checkGlobalGuidance(
	t *testing.T,
	actor permissionGuidanceActor,
	path, global string,
) {
	t.Helper()
	if !roles.AtLeast(global, "maintainer") {
		page := f.request(t, "GET", path, "", "", actor.session, false, 200)
		if !strings.Contains(
			string(page),
			"API tokens require a global Maintainer or Admin role.",
		) {
			t.Fatal("account menu does not explain token eligibility")
		}
	}
	if global == "maintainer" {
		denied := string(f.request(t, "PUT", path, "name=blocked", "",
			actor.session, false, 403))
		if !strings.Contains(denied, path+"/edit#project-members") ||
			strings.Contains(denied, `href="/admin/users"`) {
			t.Fatal("write denial points to the wrong role settings")
		}
		f.request(t, "PUT", path, "name=blocked", "", actor.session, true, 200)
	}
}

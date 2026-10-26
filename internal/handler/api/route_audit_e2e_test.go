//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/roles"
)

type routeAuditCase struct {
	f                permissionFixture
	project, foreign db.Project
	target           *db.User
	count            func() int
	assertEvent      func(int, bool, int64, int64, string, string)
}

func TestRouteAuditAuthorizationAPIWebE2E(t *testing.T) {
	f := newPermissionFixture(t)
	project, foreign := seedProject(t, f.h.repo), seedProject(t, f.h.repo)
	target := seedAPIUser(t, f.h.repo, "audit-target@example.test", "viewer")
	count := func() int {
		t.Helper()
		var n int
		if err := f.h.repo.DB.QueryRowContext(t.Context(),
			"SELECT COUNT(*) FROM audit_log").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	assertEvent := func(before int, allowed bool, userID, entityID int64, action, entity string) {
		t.Helper()
		want := before
		if allowed {
			want++
		}
		if got := count(); got != want {
			t.Fatalf("audit count=%d want=%d", got, want)
		}
		if !allowed {
			return
		}
		rows, err := f.h.repo.Queries.ListAuditLogsFiltered(
			t.Context(),
			db.ListAuditLogsFilteredParams{PageLimit: 1},
		)
		if err != nil || len(rows) != 1 {
			t.Fatalf("audit rows=%v: %v", rows, err)
		}
		event := rows[0]
		if event.Action != action || event.EntityType != entity ||
			!event.UserID.Valid || event.UserID.Int64 != userID ||
			!event.EntityID.Valid || event.EntityID.Int64 != entityID {
			t.Fatalf("incorrect audit attribution: %+v", event)
		}
	}
	a := routeAuditCase{
		f: f, project: project, foreign: foreign, target: target,
		count: count, assertEvent: assertEvent,
	}
	for _, role := range roles.All() {
		t.Run(role, func(t *testing.T) {
			a.checkRole(t, role)
		})
	}
	owner := seedAPIUser(t, f.h.repo, "scoped@route-audit.test", "admin")
	for _, ceiling := range roles.All() {
		t.Run("scoped/"+ceiling, func(t *testing.T) {
			a.checkScoped(t, owner, ceiling)
		})
	}
}

func (a routeAuditCase) checkRole(t *testing.T, role string) {
	t.Helper()
	f, project := a.f, a.project
	count, assertEvent := a.count, a.assertEvent
	user := seedAPIUser(t, f.h.repo, role+"@route-audit.test", role)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: project.ID, UserID: user.ID, Role: role,
	}); err != nil {
		t.Fatal(err)
	}
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	session := memberRoleSession(t, f.h, user.ID)
	for _, surface := range []string{"API", "web"} {
		a.checkProjectWrite(t, role, surface, user, token, session)
		a.checkAdminWrite(t, role, surface, user, token, session)
	}
	// Logout records directly and suppresses the middleware duplicate.
	before := count()
	f.request(t, "POST", "/logout", "", "", session, false, 200)
	assertEvent(before, true, user.ID, user.ID, "logout", "user")
}

func (a routeAuditCase) checkProjectWrite(
	t *testing.T, role, surface string, user *db.User, token, session string,
) {
	t.Helper()
	f, project := a.f, a.project
	count, assertEvent := a.count, a.assertEvent
	allowed := role == "maintainer" || role == "admin"
	before := count()
	original, err := f.h.repo.Queries.GetProject(
		t.Context(),
		project.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	name := role + "-" + surface
	path := fmt.Sprintf("/projects/%d", project.ID)
	want := 403
	if allowed {
		want = 200
	}
	if surface == "API" {
		f.request(
			t,
			"PUT",
			"/api/v1"+path,
			fmt.Sprintf(`{"name":%q}`, name),
			token,
			"",
			false,
			want,
		)
	} else {
		f.request(t, "PUT", path, url.Values{"name": {name}}.Encode(), "", session, false, want)
	}
	assertEvent(
		before,
		allowed,
		user.ID,
		project.ID,
		"update_project",
		"project",
	)
	saved, err := f.h.repo.Queries.GetProject(
		t.Context(),
		project.ID,
	)
	if err != nil || allowed && saved.Name != name ||
		!allowed && saved != original {
		t.Fatalf(
			"project write allowed=%v saved=%+v original=%+v: %v",
			allowed,
			saved,
			original,
			err,
		)
	}
	if role == "viewer" && surface == "web" {
		f.request(
			t,
			"PUT",
			path,
			"name=forged",
			"",
			session,
			true,
			200,
		)
		assertEvent(before, false, user.ID, project.ID, "", "")
	}
}

func (a routeAuditCase) checkAdminWrite(
	t *testing.T, role, surface string, user *db.User, token, session string,
) {
	t.Helper()
	f, target := a.f, a.target
	count, assertEvent := a.count, a.assertEvent
	// A global administrator gate remains stronger than content authoring.
	before := count()
	originalUser, err := f.h.repo.Queries.GetUserByID(
		t.Context(),
		target.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/admin/users/%d", target.ID)
	want := 403
	if role == "admin" {
		want = 200
	}
	if surface == "API" {
		f.request(t, "PUT", "/api/v1"+path, fmt.Sprintf(
			`{"name":"Audit target","email":%q,"role":"viewer"}`,
			target.Email,
		), token, "", false, want)
	} else {
		f.request(t, "PUT", path, url.Values{"name": {"Audit target"}, "email": {target.Email}, "role": {"viewer"}}.Encode(), "", session, false, want)
	}
	assertEvent(
		before,
		role == "admin",
		user.ID,
		target.ID,
		"update_user",
		"user",
	)
	savedUser, err := f.h.repo.Queries.GetUserByID(
		t.Context(),
		target.ID,
	)
	if err != nil || role != "admin" && savedUser != originalUser {
		t.Fatalf(
			"rejected admin write changed user: %+v: %v",
			savedUser,
			err,
		)
	}
}

func (a routeAuditCase) checkScoped(
	t *testing.T, owner *db.User, ceiling string,
) {
	t.Helper()
	f, project, foreign, target := a.f, a.project, a.foreign, a.target
	count, assertEvent := a.count, a.assertEvent
	id, token := seedAPIToken(t, f.h.repo, owner.ID)
	if _, err := f.h.repo.ReplaceTokenScope(t.Context(), id, repository.TokenScope{
		Scope: "scoped", Role: ceiling, ProjectIDs: []int64{project.ID},
	}); err != nil {
		t.Fatal(err)
	}
	allowed := ceiling == "maintainer" || ceiling == "admin"
	before := count()
	want := 403
	if allowed {
		want = 200
	}
	f.request(
		t,
		"PUT",
		fmt.Sprintf("/api/v1/projects/%d", project.ID),
		`{"name":"scoped author"}`,
		token,
		"",
		false,
		want,
	)
	assertEvent(
		before,
		allowed,
		owner.ID,
		project.ID,
		"update_project",
		"project",
	)
	before = count()
	f.request(
		t,
		"PUT",
		fmt.Sprintf("/api/v1/projects/%d", foreign.ID),
		`{"name":"escaped"}`,
		token,
		"",
		false,
		403,
	)
	f.request(
		t,
		"PUT",
		fmt.Sprintf("/api/v1/admin/users/%d", target.ID),
		`{}`,
		token,
		"",
		false,
		403,
	)
	assertEvent(before, false, owner.ID, foreign.ID, "", "")
	saved, err := f.h.repo.Queries.GetProject(t.Context(), foreign.ID)
	if err != nil || saved != foreign {
		t.Fatalf(
			"scoped write changed foreign project: %+v: %v",
			saved,
			err,
		)
	}
}

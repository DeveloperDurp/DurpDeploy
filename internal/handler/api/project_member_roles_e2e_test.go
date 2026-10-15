package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

type memberRoleActor struct {
	name, global, project string
	allowed               bool
}

type memberRoleAuth struct {
	token, session string
	hx, csrf       bool
}

type memberRoleFixture struct {
	h                       *harness
	base                    string
	client                  *http.Client
	project, other          db.Project
	target                  *db.User
	original, otherOriginal db.ProjectMember
}

func memberRoleSession(t *testing.T, h *harness, userID int64) string {
	t.Helper()
	session := fmt.Sprintf("member-role-%d", userID)
	if _, err := h.repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{
			ID: session, UserID: userID, CsrfToken: "member-role-csrf",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}); err != nil {
		t.Fatal(err)
	}
	return session
}

func TestProjectMemberRolesAPIWebE2E(t *testing.T) {
	h := newAPIHarness(t)
	srv := httptest.NewServer(server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Month|cron.Dom|cron.Dow),
		handler.NewAuthHandler(h.repo)))
	t.Cleanup(srv.Close)
	f := &memberRoleFixture{
		h: h, base: srv.URL,
		project: seedProject(t, h.repo), other: seedProject(t, h.repo),
		target: seedAPIUser(t, h.repo, "target@member.test", "deployer"),
		client: &http.Client{CheckRedirect: func(
			*http.Request, []*http.Request,
		) error {
			return http.ErrUseLastResponse
		}},
	}
	f.add(t, f.project.ID, f.target.ID, "deployer")
	f.add(t, f.other.ID, f.target.ID, "admin")
	f.original, f.otherOriginal = f.member(
		t,
		f.project.ID,
	), f.member(
		t,
		f.other.ID,
	)
	for _, actor := range []memberRoleActor{
		{"global-admin", "admin", "", true},
		{"project-admin", "maintainer", "admin", true},
		{"restricted-project-admin", "deployer", "admin", false},
		{"project-deployer", "deployer", "deployer", false},
		{"viewer-project-admin", "viewer", "admin", false},
		{"other-project-admin", "deployer", "", false},
	} {
		t.Run(actor.name, func(t *testing.T) { f.checkActor(t, actor) })
	}
	path := fmt.Sprintf("/projects/%d/members/%d", f.project.ID, f.target.ID)
	f.request(t, "/api/v1"+path, `{"role":"admin"}`, memberRoleAuth{}, 401)
	f.request(t, path, "role=admin", memberRoleAuth{}, 303)
	if f.member(t, f.other.ID) != f.otherOriginal {
		t.Fatal("role update changed another project membership")
	}
	user, err := h.repo.Queries.GetUserByID(t.Context(), f.target.ID)
	if err != nil || user.Role != "deployer" {
		t.Fatalf("role update changed global role: %+v, %v", user, err)
	}
}

func (f *memberRoleFixture) add(
	t *testing.T, projectID, userID int64, role string,
) {
	t.Helper()
	if err := f.h.repo.Queries.AddProjectMember(t.Context(),
		db.AddProjectMemberParams{
			ProjectID: projectID, UserID: userID, Role: role,
		}); err != nil {
		t.Fatal(err)
	}
}

func (f *memberRoleFixture) member(
	t *testing.T, projectID int64,
) db.ProjectMember {
	t.Helper()
	m, err := f.h.repo.Queries.GetProjectMember(t.Context(),
		db.GetProjectMemberParams{ProjectID: projectID, UserID: f.target.ID})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func (f *memberRoleFixture) request(
	t *testing.T, path, body string, auth memberRoleAuth, want int,
) (*http.Response, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "PUT",
		f.base+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if auth.token != "" {
		req.Header.Set("Authorization", "Bearer "+auth.token)
		req.Header.Set("Content-Type", "application/json")
	} else {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	if auth.session != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: auth.session})
	}
	if auth.csrf {
		req.Header.Set("X-CSRF-Token", "member-role-csrf")
	}
	if auth.hx {
		req.Header.Set("HX-Request", "true")
		req.Header.Set("X-Form-Dialog", "true")
	}
	resp, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil || resp.StatusCode != want {
		t.Fatalf("PUT %s: status=%d body=%s error=%v",
			path, resp.StatusCode, data, err)
	}
	return resp, string(data)
}

func (f *memberRoleFixture) checkActor(t *testing.T, actor memberRoleActor) {
	t.Helper()
	user := seedAPIUser(t, f.h.repo, actor.name+"@member.test", actor.global)
	f.add(t, f.other.ID, user.ID, "admin")
	if actor.project != "" {
		f.add(t, f.project.ID, user.ID, actor.project)
	}
	_, token := seedAPIToken(t, f.h.repo, user.ID)
	session := memberRoleSession(t, f.h, user.ID)
	for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
		if actor.allowed {
			f.checkAllowedRole(t, role, token, session)
		} else {
			f.checkDeniedRole(t, role, token, session)
		}
	}
	if actor.allowed {
		f.checkInvalidRequests(t, user.ID, actor.project != "", token, session)
	}
}

func (f *memberRoleFixture) checkAllowedRole(
	t *testing.T, role, token, session string,
) {
	t.Helper()
	path := fmt.Sprintf("/projects/%d/members/%d", f.project.ID, f.target.ID)
	before := f.member(t, f.project.ID)
	f.request(t, "/api/v1"+path, `{"role":"`+role+`"}`,
		memberRoleAuth{token: token}, 200)
	if f.member(t, f.project.ID).Role != role {
		t.Fatal("API did not change the role")
	}
	f.add(t, f.project.ID, f.target.ID, before.Role)
	web := memberRoleAuth{session: session, csrf: true}
	resp, _ := f.request(t, path, "role="+role, web, 303)
	if resp.Header.Get(
		"Location",
	) != fmt.Sprintf(
		"/projects/%d/edit",
		f.project.ID,
	) {
		t.Fatal("web redirect did not return to project edit")
	}
	if f.member(t, f.project.ID).Role != role {
		t.Fatal("web request did not change the role")
	}
	f.add(t, f.project.ID, f.target.ID, before.Role)
	web.hx = true
	resp, body := f.request(t, path, "role="+role, web, 200)
	if !strings.Contains(body, `id="project-members"`) ||
		!strings.Contains(body, "data-member-role>"+role) ||
		!strings.Contains(
			resp.Header.Get("HX-Trigger"),
			"Member role updated",
		) ||
		resp.Header.Get("HX-Redirect") != "" {
		t.Fatal("role save did not return a success fragment")
	}
	if got := f.member(t, f.project.ID); got.Role != role ||
		got.CreatedAt != f.original.CreatedAt {
		t.Fatalf("role update replaced membership: %+v", got)
	}
}

func (f *memberRoleFixture) checkDeniedRole(
	t *testing.T, role, token, session string,
) {
	t.Helper()
	path := fmt.Sprintf("/projects/%d/members/%d", f.project.ID, f.target.ID)
	before := f.member(t, f.project.ID)
	f.request(t, "/api/v1"+path, `{"role":"`+role+`"}`,
		memberRoleAuth{token: token}, 403)
	web := memberRoleAuth{session: session, csrf: true}
	f.request(t, path, "role="+role, web, 403)
	web.hx = true
	f.request(t, path, "role="+role, web, 200)
	if f.member(t, f.project.ID) != before {
		t.Fatal("denied role update changed membership")
	}
}

func (f *memberRoleFixture) checkInvalidRequests(
	t *testing.T, userID int64, isMember bool, token, session string,
) {
	t.Helper()
	path := fmt.Sprintf("/projects/%d/members/%d", f.project.ID, f.target.ID)
	api := memberRoleAuth{token: token}
	web := memberRoleAuth{session: session, hx: true, csrf: true}
	for _, role := range []string{"", "owner"} {
		f.request(t, "/api/v1"+path, `{"role":"`+role+`"}`, api, 422)
		f.request(t, path, "role="+role, web, 422)
	}
	f.request(t, path, "role=admin", memberRoleAuth{session: session}, 403)
	invalid := fmt.Sprintf("/projects/%d/members/invalid", f.project.ID)
	f.request(t, "/api/v1"+invalid, `{"role":"admin"}`, api, 400)
	f.request(t, invalid, "role=admin", web, 400)
	for _, suffix := range []string{"999999", fmt.Sprint(userID)} {
		// Global admins need no membership in the managed project.
		if suffix == fmt.Sprint(userID) && isMember {
			continue
		}
		missing := fmt.Sprintf("/projects/%d/members/%s", f.project.ID, suffix)
		f.request(t, "/api/v1"+missing, `{"role":"admin"}`, api, 404)
		f.request(t, missing, "role=admin", web, 404)
	}
	if f.member(t, f.project.ID).Role != "admin" {
		t.Fatal("invalid request changed the role")
	}
	var audited int
	err := f.h.repo.DB.QueryRowContext(t.Context(),
		`SELECT COUNT(*) FROM audit_log
		 WHERE user_id = ? AND action = 'update_project_member'`,
		userID,
	).Scan(&audited)
	if err != nil || audited != 12 {
		t.Fatalf("successful role changes audited=%d, error=%v", audited, err)
	}
}

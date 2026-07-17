package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// seedSessionAs is like seedSession but takes a custom email so multiple
// users with the same role can coexist in one test DB (seedSession
// hardcodes email as role+"@test.local", which collides on the UNIQUE
// constraint when two deployers are needed).
func seedSessionAs(
	t *testing.T,
	repo *repository.Repository,
	serverURL, email, role string,
) *authedSession {
	t.Helper()
	ctx := context.Background()

	hash, err := auth.HashPassword("testpass")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		Name:         email,
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user %q: %v", email, err)
	}

	token, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("new session token: %v", err)
	}
	expiresAt := time.Now().Add(24 * time.Hour).Unix()
	if _, err := repo.Queries.CreateSession(ctx, db.CreateSessionParams{
		ID:        token,
		UserID:    u.ID,
		CsrfToken: csrf,
		ExpiresAt: expiresAt,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	u2, _ := url.Parse(serverURL)
	jar.SetCookies(u2, []*http.Cookie{{Name: "session", Value: token}})

	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	return &authedSession{
		user:         &u,
		sessionToken: token,
		csrfToken:    csrf,
		client:       client,
	}
}

// TestCreateProject_AutoAddsCreatorAsAdmin verifies that POST /projects
// creates both a project row and a project_members row for
// (project_id, creator_id, 'admin'). Without the auto-add, the creator
// is locked out of their own project by RequireProjectAccess.
func TestCreateProject_AutoAddsCreatorAsAdmin(t *testing.T) {
	h := newProjectHarness(t)
	// h.sess is an admin session — but the auto-add should fire for
	// any role, so switch to a deployer to prove it's not admin-only.
	h.setRole("deployer")

	form := url.Values{
		"name":        {"auto-add-proj"},
		"description": {""},
	}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(h.server.URL+"/projects", form)
	if err != nil {
		t.Fatalf("POST /projects: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}

	projects, err := h.repo.Queries.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	var projID int64
	found := false
	for _, p := range projects {
		if p.Name == "auto-add-proj" {
			projID = p.ID
			found = true
			break
		}
	}
	if !found {
		t.Fatal("project 'auto-add-proj' not found in DB")
	}

	member, err := h.repo.Queries.GetProjectMember(
		context.Background(),
		db.GetProjectMemberParams{
			ProjectID: projID,
			UserID:    h.sess.user.ID,
		},
	)
	if err != nil {
		t.Fatalf("project_members row missing for creator: %v", err)
	}
	if member.Role != "admin" {
		t.Fatalf("creator member role = %q, want %q", member.Role, "admin")
	}
}

// TestProjectsList_NonAdminSeesOnlyMemberProjects verifies that a
// non-admin user only sees projects they are a member of on GET
// /projects. The admin sees all projects.
func TestProjectsList_NonAdminSeesOnlyMemberProjects(t *testing.T) {
	h := newProjectHarness(t)

	// Admin creates two projects (auto-adds admin as member of both).
	projA := h.makeProject("alpha")
	h.makeProject("beta") // projB: deployer is NOT a member

	// Create a deployer session.
	deployer := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"deployer-a@test.local",
		"deployer",
	)

	// Add deployer as a member of projA only.
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: projA.ID,
			UserID:    deployer.user.ID,
			Role:      "deployer",
		},
	); err != nil {
		t.Fatalf("add member: %v", err)
	}

	// Deployer's project list should contain projA but not projB.
	deployerClient := deployer.client
	resp, err := deployerClient.Get(h.server.URL + "/projects")
	if err != nil {
		t.Fatalf("GET /projects (deployer): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(
			"deployer GET /projects: status = %d, want 200",
			resp.StatusCode,
		)
	}
	body := readBody(t, resp)

	if !strings.Contains(body, "alpha") {
		t.Errorf("deployer should see project 'alpha' (member)")
	}
	if strings.Contains(body, "beta") {
		t.Errorf("deployer should NOT see project 'beta' (not a member)")
	}

	// Admin sees both.
	adminResp, err := h.authedClient().Get(h.server.URL + "/projects")
	if err != nil {
		t.Fatalf("GET /projects (admin): %v", err)
	}
	defer adminResp.Body.Close()
	adminBody := readBody(t, adminResp)
	if !strings.Contains(adminBody, "alpha") ||
		!strings.Contains(adminBody, "beta") {
		t.Errorf("admin should see both projects 'alpha' and 'beta'")
	}
}

// TestProjectMembers_AddRemoveByAdmin verifies the members add/remove
// endpoints work for a global admin, and that the audit log records
// the actions.
func TestProjectMembers_AddRemoveByAdmin(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("members-admin")

	// Create a deployer to add as a member.
	deployer := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"mem-deployer@test.local",
		"deployer",
	)

	// Add the deployer as a member via POST /projects/{id}/members.
	form := url.Values{
		"user_id": {strconv.FormatInt(deployer.user.ID, 10)},
		"role":    {"deployer"},
	}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/members", h.server.URL, proj.ID),
		form,
	)
	if err != nil {
		t.Fatalf("POST members: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("add member: status = %d, want 303", resp.StatusCode)
	}

	// Verify the member row exists.
	member, err := h.repo.Queries.GetProjectMember(
		context.Background(),
		db.GetProjectMemberParams{
			ProjectID: proj.ID,
			UserID:    deployer.user.ID,
		},
	)
	if err != nil {
		t.Fatalf("member row missing after add: %v", err)
	}
	if member.Role != "deployer" {
		t.Fatalf("member role = %q, want %q", member.Role, "deployer")
	}

	// Verify the audit log recorded add_project_member.
	entries, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 50,
			FAction: sql.NullString{
				String: "add_project_member",
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("audit log missing add_project_member entry")
	}

	// Remove the member via DELETE /projects/{id}/members/{userId}.
	delReq, err := http.NewRequest(
		"DELETE",
		fmt.Sprintf(
			"%s/projects/%d/members/%d",
			h.server.URL,
			proj.ID,
			deployer.user.ID,
		),
		strings.NewReader("csrf_token="+h.csrfToken()),
	)
	if err != nil {
		t.Fatalf("new DELETE request: %v", err)
	}
	delReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	delReq.Header.Set("X-CSRF-Token", h.csrfToken())
	delResp, err := h.authedClient().Do(delReq)
	if err != nil {
		t.Fatalf("DELETE member: %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("remove member: status = %d, want 303", delResp.StatusCode)
	}

	// Verify the member row is gone.
	_, err = h.repo.Queries.GetProjectMember(
		context.Background(),
		db.GetProjectMemberParams{
			ProjectID: proj.ID,
			UserID:    deployer.user.ID,
		},
	)
	if err == nil {
		t.Fatal("member row still exists after remove")
	}

	// Verify the audit log recorded remove_project_member.
	entries, err = h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 50,
			FAction: sql.NullString{
				String: "remove_project_member",
				Valid:  true,
			},
		},
	)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("audit log missing remove_project_member entry")
	}
}

// TestProjectMembers_PerProjectDeployerCannotManage verifies that a
// per-project deployer (not a per-project admin) can see the member
// list but gets 403 on POST (add) and DELETE (remove).
func TestProjectMembers_PerProjectDeployerCannotManage(t *testing.T) {
	h := newProjectHarness(t)
	proj := h.makeProject("members-gate")

	// Create two deployers: one is a per-project admin, one is a
	// per-project deployer.
	projAdmin := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"proj-admin@test.local",
		"deployer",
	)
	projDeployer := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"proj-deployer@test.local",
		"deployer",
	)

	// Add both as members — projAdmin as admin, projDeployer as deployer.
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: proj.ID, UserID: projAdmin.user.ID, Role: "admin",
		},
	); err != nil {
		t.Fatalf("add projAdmin: %v", err)
	}
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: proj.ID, UserID: projDeployer.user.ID, Role: "deployer",
		},
	); err != nil {
		t.Fatalf("add projDeployer: %v", err)
	}

	// Create a third user to attempt to add.
	target := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"target-deployer@test.local",
		"deployer",
	)

	// projDeployer (per-project deployer) gets 403 on POST add.
	form := url.Values{
		"user_id": {strconv.FormatInt(target.user.ID, 10)},
		"role":    {"deployer"},
	}
	form.Set("csrf_token", projDeployer.csrfToken)
	resp, err := projDeployer.client.PostForm(
		fmt.Sprintf("%s/projects/%d/members", h.server.URL, proj.ID),
		form,
	)
	if err != nil {
		t.Fatalf("POST members (deployer): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"per-project deployer add: status = %d, want 403",
			resp.StatusCode,
		)
	}

	// projDeployer gets 403 on DELETE remove.
	delReq, err := http.NewRequest(
		"DELETE",
		fmt.Sprintf(
			"%s/projects/%d/members/%d",
			h.server.URL,
			proj.ID,
			projAdmin.user.ID,
		),
		strings.NewReader("csrf_token="+projDeployer.csrfToken),
	)
	if err != nil {
		t.Fatalf("new DELETE: %v", err)
	}
	delReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	delReq.Header.Set("X-CSRF-Token", projDeployer.csrfToken)
	delResp, err := projDeployer.client.Do(delReq)
	if err != nil {
		t.Fatalf("DELETE (deployer): %v", err)
	}
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"per-project deployer remove: status = %d, want 403",
			delResp.StatusCode,
		)
	}

	// projAdmin (per-project admin) can add.
	form.Set("csrf_token", projAdmin.csrfToken)
	resp, err = projAdmin.client.PostForm(
		fmt.Sprintf("%s/projects/%d/members", h.server.URL, proj.ID),
		form,
	)
	if err != nil {
		t.Fatalf("POST members (projAdmin): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"per-project admin add: status = %d, want 303",
			resp.StatusCode,
		)
	}
}

// TestDeployAuthorization is the P1-1 acceptance test: deployer A in
// project P can deploy; deployer B (not in P) gets 403; admin can
// deploy to any project.
func TestDeployAuthorization(t *testing.T) {
	h := newProjectHarness(t)

	// Admin creates a project, env, and release.
	proj := h.makeProject("authz-proj")
	env := h.makeEnv("authz-env")
	rel := h.makeRelease(proj.ID, "1.0.0", "exit 0")

	// Create deployer A and deployer B.
	deployerA := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"deployer-a@test.local",
		"deployer",
	)
	deployerB := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"deployer-b@test.local",
		"deployer",
	)

	// Add deployer A as a member of the project. Deployer B is NOT a member.
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: proj.ID,
			UserID:    deployerA.user.ID,
			Role:      "deployer",
		},
	); err != nil {
		t.Fatalf("add deployerA: %v", err)
	}

	deployURL := fmt.Sprintf("%s/projects/%d/deploy", h.server.URL, proj.ID)

	// Deployer A (member) → 303 (success).
	formA := url.Values{
		"release_id":     {strconv.FormatInt(rel.ID, 10)},
		"environment_id": {strconv.FormatInt(env.ID, 10)},
	}
	formA.Set("csrf_token", deployerA.csrfToken)
	respA, err := deployerA.client.PostForm(deployURL, formA)
	if err != nil {
		t.Fatalf("deploy A: %v", err)
	}
	defer respA.Body.Close()
	if respA.StatusCode != http.StatusSeeOther {
		t.Fatalf("deployer A (member): status = %d, want 303", respA.StatusCode)
	}

	// Deployer B (not a member) → 403.
	formB := url.Values{
		"release_id":     {strconv.FormatInt(rel.ID, 10)},
		"environment_id": {strconv.FormatInt(env.ID, 10)},
	}
	formB.Set("csrf_token", deployerB.csrfToken)
	respB, err := deployerB.client.PostForm(deployURL, formB)
	if err != nil {
		t.Fatalf("deploy B: %v", err)
	}
	defer respB.Body.Close()
	if respB.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"deployer B (not a member): status = %d, want 403",
			respB.StatusCode,
		)
	}

	// Admin (no membership row needed) → 303 (success).
	formAdmin := url.Values{
		"release_id":     {strconv.FormatInt(rel.ID, 10)},
		"environment_id": {strconv.FormatInt(env.ID, 10)},
	}
	formAdmin.Set("csrf_token", h.csrfToken())
	respAdmin, err := h.authedClient().PostForm(deployURL, formAdmin)
	if err != nil {
		t.Fatalf("deploy admin: %v", err)
	}
	defer respAdmin.Body.Close()
	if respAdmin.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin: status = %d, want 303", respAdmin.StatusCode)
	}
}

// readBody is a small helper to read an HTTP response body as a string.
func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		t.Fatalf("read body: %v", err)
	}
	return buf.String()
}

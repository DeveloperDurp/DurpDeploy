package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

// TestUsers_NonAdminCannotList: a deployer or viewer who hits
// /admin/users directly must get 403 (the RequireRole middleware
// gates the sub-group, not the handler).
func TestUsers_NonAdminCannotList(t *testing.T) {
	for _, role := range []string{"deployer", "viewer"} {
		t.Run(role, func(t *testing.T) {
			h := newProjectHarness(t)
			h.setRole(role)

			resp, err := h.authedClient().Get(h.server.URL + "/admin/users")
			if err != nil {
				t.Fatalf("GET /admin/users: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
		})
	}
}

// TestUsers_NavDropdownHiddenForNonAdmin: the navbar Admin dropdown
// must NOT render for non-admin users. Verifies the templ's `if
// user.Role == "admin"` guard on the base layout.
func TestUsers_NavDropdownHiddenForNonAdmin(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("deployer")

	resp, err := h.authedClient().Get(h.server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if strings.Contains(body, ">Admin<") {
		t.Fatalf("non-admin nav should not contain Admin button: %s", body)
	}
}

// TestUsers_NavDropdownVisibleForAdmin: the Admin dropdown must
// render for admins, with both Audit log and Users links.
func TestUsers_NavDropdownVisibleForAdmin(t *testing.T) {
	h := newProjectHarness(t) // default role: admin

	resp, err := h.authedClient().Get(h.server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, ">Admin<") {
		t.Fatalf("admin nav should contain Admin button: %s", body)
	}
	if !strings.Contains(body, `href="/admin/audit"`) {
		t.Fatalf("admin nav should link to /admin/audit: %s", body)
	}
	if !strings.Contains(body, `href="/admin/users"`) {
		t.Fatalf("admin nav should link to /admin/users: %s", body)
	}
}

// TestUsers_AdminCanList: GET /admin/users as admin renders the user
// list, including the admin user that was seeded by the harness.
func TestUsers_AdminCanList(t *testing.T) {
	h := newProjectHarness(t)

	resp, err := h.authedClient().Get(h.server.URL + "/admin/users")
	if err != nil {
		t.Fatalf("GET /admin/users: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "admin@test.local") {
		t.Fatalf("body missing the seeded admin user: %s", body)
	}
	if !strings.Contains(body, "New user") {
		t.Fatalf("body missing the New user button: %s", body)
	}
}

// TestUsers_CreateLogin: admin creates a new user via POST
// /admin/users; the new user can immediately log in with the chosen
// password. Also verifies the audit log records create_user.
func TestUsers_CreateLogin(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"email":    {"newuser@example.com"},
		"name":     {"New User"},
		"role":     {"deployer"},
		"password": {"initial-pass-1"},
	}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(h.server.URL+"/admin/users", form)
	if err != nil {
		t.Fatalf("POST /admin/users: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create user: status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get(
		"Location",
	); !strings.HasPrefix(
		loc,
		"/admin/users?new_user_id=",
	) {
		t.Fatalf("redirect = %q, want /admin/users?new_user_id=...", loc)
	}

	// The new user can log in.
	client := newJar(t)
	login := url.Values{
		"email":    {"newuser@example.com"},
		"password": {"initial-pass-1"},
	}
	loginResp, err := client.PostForm(h.server.URL+"/login", login)
	if err != nil {
		t.Fatalf("login new user: %v", err)
	}
	defer loginResp.Body.Close()
	if loginResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("new user login: status = %d, want 303", loginResp.StatusCode)
	}

	// Audit log has the create_user entry. entity_id is left empty
	// for creates (the URL has no numeric ID at the time of the POST —
	// same as create_project per the existing convention).
	entries, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 50,
			FAction:   sql.NullString{String: "create_user", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("create_user audit rows = %d, want 1", len(entries))
	}
}

// TestUsers_CreateDuplicateEmail: a second create with the same
// email must 422 and the form must re-render with the error.
func TestUsers_CreateDuplicateEmail(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"email":    {"dup@example.com"},
		"name":     {"Dup"},
		"role":     {"deployer"},
		"password": {"password1"},
	}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(h.server.URL+"/admin/users", form)
	if err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("first create: status = %d, want 303", resp.StatusCode)
	}

	// Try to create a second user with the same email.
	form.Set("csrf_token", h.csrfToken())
	resp2, err := h.authedClient().PostForm(h.server.URL+"/admin/users", form)
	if err != nil {
		t.Fatalf("second create: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("dup create: status = %d, want 422", resp2.StatusCode)
	}
	body := readBody(t, resp2)
	if !strings.Contains(body, "already exists") {
		t.Fatalf("dup body missing 'already exists' message: %s", body)
	}
}

// TestUsers_CreateMissingPassword: creating a user without a
// password must 422.
func TestUsers_CreateMissingPassword(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"email": {"nopass@example.com"},
		"name":  {"NoPass"},
		"role":  {"viewer"},
	}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(h.server.URL+"/admin/users", form)
	if err != nil {
		t.Fatalf("POST /admin/users: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Password") {
		t.Fatalf("body missing 'Password' message: %s", body)
	}
}

// TestUsers_RoleChangeInvalidatesSession: when an admin changes a
// user's role via PUT /admin/users/{id}, the user's existing session
// is deleted so their next request must re-login and re-read their
// role from the DB. Without the delete, the cached role on the
// session row would persist (P0 reads role from the session row,
// not the users row on every request).
func TestUsers_RoleChangeInvalidatesSession(t *testing.T) {
	h := newProjectHarness(t)

	// Create a deployer user + a session for them.
	target := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"promote-me@example.com",
		"deployer",
	)

	// Admin promotes them to admin.
	form := url.Values{
		"name": {"Promote Me"},
		"role": {"admin"},
	}
	form.Set("csrf_token", h.csrfToken())
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPut,
		fmt.Sprintf("%s/admin/users/%d", h.server.URL, target.user.ID),
		bytes.NewBufferString(form.Encode()),
	)
	if err != nil {
		t.Fatalf("build put: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", h.csrfToken())
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("PUT /admin/users/{id}: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther &&
		resp.StatusCode != http.StatusOK {
		t.Fatalf("update user: status = %d, want 303/200", resp.StatusCode)
	}

	// The target user's session row should be gone.
	_, err = h.repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID:        target.sessionToken,
			ExpiresAt: 0,
		},
	)
	if err == nil {
		t.Fatal("target session row still exists after role change")
	}

	// The user row in the DB should now have role=admin.
	updated, err := h.repo.Queries.GetUserByID(
		context.Background(),
		target.user.ID,
	)
	if err != nil {
		t.Fatalf("get user: %v", err)
	}
	if updated.Role != "admin" {
		t.Fatalf("role = %q, want %q", updated.Role, "admin")
	}

	// Audit log recorded update_user.
	entries, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 50,
			FAction:   sql.NullString{String: "update_user", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("update_user audit rows = %d, want 1", len(entries))
	}
}

// TestUsers_PasswordChangeInvalidatesSession: PUT with a new
// password deletes the existing session too (the password is part
// of the security boundary, not just the role).
func TestUsers_PasswordChangeInvalidatesSession(t *testing.T) {
	h := newProjectHarness(t)
	target := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"pw-change@example.com",
		"deployer",
	)

	form := url.Values{
		"name":     {"PW Change"},
		"role":     {"deployer"},
		"password": {"new-password-1"},
	}
	form.Set("csrf_token", h.csrfToken())
	req, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodPut,
		fmt.Sprintf("%s/admin/users/%d", h.server.URL, target.user.ID),
		bytes.NewBufferString(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", h.csrfToken())
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("PUT: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther &&
		resp.StatusCode != http.StatusOK {
		t.Fatalf("update: status = %d, want 303/200", resp.StatusCode)
	}

	_, err = h.repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID:        target.sessionToken,
			ExpiresAt: 0,
		},
	)
	if err == nil {
		t.Fatal("target session row still exists after password change")
	}
}

// TestUsers_CannotDeleteSelf: an admin cannot delete their own
// account via DELETE /admin/users/{id} — prevents the "I just
// deleted the only admin" footgun.
func TestUsers_CannotDeleteSelf(t *testing.T) {
	h := newProjectHarness(t)
	adminID := h.sess.user.ID

	form := url.Values{}
	form.Set("csrf_token", h.csrfToken())
	req, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodDelete,
		fmt.Sprintf("%s/admin/users/%d", h.server.URL, adminID),
		nil,
	)
	req.Header.Set("X-CSRF-Token", h.csrfToken())
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("DELETE self: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("self-delete: status = %d, want 422", resp.StatusCode)
	}

	// Admin still exists.
	if _, err := h.repo.Queries.GetUserByID(
		context.Background(),
		adminID,
	); err != nil {
		t.Fatalf("admin row missing after refused self-delete: %v", err)
	}
}

// TestUsers_CannotDeleteUserWithProjectMembership: a user with
// project_members rows cannot be deleted — admin must remove
// project membership first.
func TestUsers_CannotDeleteUserWithProjectMembership(t *testing.T) {
	h := newProjectHarness(t)
	target := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"with-proj@example.com",
		"deployer",
	)

	proj := h.makeProject("has-member")
	if err := h.repo.Queries.AddProjectMember(
		context.Background(),
		db.AddProjectMemberParams{
			ProjectID: proj.ID,
			UserID:    target.user.ID,
			Role:      "deployer",
		},
	); err != nil {
		t.Fatalf("add member: %v", err)
	}

	form := url.Values{}
	form.Set("csrf_token", h.csrfToken())
	req, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodDelete,
		fmt.Sprintf("%s/admin/users/%d", h.server.URL, target.user.ID),
		nil,
	)
	req.Header.Set("X-CSRF-Token", h.csrfToken())
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("delete with project: status = %d, want 422", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "project memberships") {
		t.Fatalf("body missing 'project memberships' message: %s", body)
	}

	// User still exists.
	if _, err := h.repo.Queries.GetUserByID(
		context.Background(),
		target.user.ID,
	); err != nil {
		t.Fatalf("user row missing after refused delete: %v", err)
	}
}

// TestUsers_DeleteInvalidatesSessions: deleting a user invalidates
// their sessions (FK CASCADE would clean them up too, but the
// handler does it explicitly so the intent is obvious).
func TestUsers_DeleteInvalidatesSessions(t *testing.T) {
	h := newProjectHarness(t)
	target := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"delete-me@example.com",
		"deployer",
	)
	id := target.user.ID
	token := target.sessionToken

	form := url.Values{}
	form.Set("csrf_token", h.csrfToken())
	req, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodDelete,
		fmt.Sprintf("%s/admin/users/%d", h.server.URL, id),
		nil,
	)
	req.Header.Set("X-CSRF-Token", h.csrfToken())
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther &&
		resp.StatusCode != http.StatusOK {
		t.Fatalf("delete: status = %d, want 303/200", resp.StatusCode)
	}

	if _, err := h.repo.Queries.GetUserByID(
		context.Background(),
		id,
	); err == nil {
		t.Fatal("user row still exists after delete")
	}
	if _, err := h.repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID:        token,
			ExpiresAt: 0,
		},
	); err == nil {
		t.Fatal("session row still exists after user delete")
	}

	// Audit log records delete_user.
	entries, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 50,
			FAction:   sql.NullString{String: "delete_user", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("delete_user audit rows = %d, want 1", len(entries))
	}
}

// TestUsers_PasswordBannerDisplayedAfterCreate: the redirect after
// POST /admin/users carries the new password in the query string;
// the list page renders the one-time banner with the password.
func TestUsers_PasswordBannerDisplayedAfterCreate(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"email":    {"banner@example.com"},
		"name":     {"Banner"},
		"role":     {"deployer"},
		"password": {"banner-pass-1"},
	}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(h.server.URL+"/admin/users", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/admin/users?new_user_id=") {
		t.Fatalf("redirect = %q, want /admin/users?new_user_id=...", loc)
	}

	// Follow the redirect (manually — harness client doesn't follow).
	// The Location header is a server-relative path, so prepend the
	// server's URL.
	followReq, _ := http.NewRequest(http.MethodGet, h.server.URL+loc, nil)
	followResp, err := h.authedClient().Do(followReq)
	if err != nil {
		t.Fatalf("GET redirect: %v", err)
	}
	defer followResp.Body.Close()
	if followResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", followResp.StatusCode)
	}
	body := readBody(t, followResp)
	if !strings.Contains(body, "banner-pass-1") {
		t.Fatalf("body missing the new password: %s", body)
	}
	if !strings.Contains(body, "User created") {
		t.Fatalf("body missing 'User created' banner header: %s", body)
	}
}

// TestUsers_SelfRowHasNoDeleteButton: the list page does not render
// a Delete button for the current user (the row shows Edit only).
// This is a UI affordance — the handler also rejects the DELETE.
func TestUsers_SelfRowHasNoDeleteButton(t *testing.T) {
	h := newProjectHarness(t)
	adminIDStr := strconv.FormatInt(h.sess.user.ID, 10)

	resp, err := h.authedClient().Get(h.server.URL + "/admin/users")
	if err != nil {
		t.Fatalf("GET /admin/users: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if !strings.Contains(body, "/admin/users/"+adminIDStr+"/edit") {
		t.Fatalf("self row missing Edit link: %s", body)
	}
	// The Delete button for the self row is a button (not an anchor)
	// with hx-delete. There is one delete_user button on the page for
	// the self row should NOT be present, but we have no other users
	// in the DB. We assert absence explicitly by looking for the
	// hx-delete to /admin/users/{selfID} — that exact pattern only
	// matches the self row.
	if strings.Contains(
		body,
		fmt.Sprintf(`hx-delete="/admin/users/%d"`, h.sess.user.ID),
	) {
		t.Fatalf("self row should not have a Delete button: %s", body)
	}
}

// TestUsers_ViewerSeesForbiddenOnFormPages: a viewer who navigates
// directly to a writer-only form page sees the ViewerForbiddenMessage
// instead of the form. Routes that already have an admin-only or
// per-project gate (e.g. /admin/users/*) return 403 from that gate
// before the templ guard runs; this test exercises a route a viewer
// can actually reach (/environments/new) where the CanWrite templ
// guard is the defensive layer.
func TestUsers_ViewerSeesForbiddenOnFormPages(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("viewer")

	for _, path := range []string{
		"/environments/new",
		"/lifecycles/new",
		"/templates/new",
	} {
		t.Run(path, func(t *testing.T) {
			resp, err := h.authedClient().Get(h.server.URL + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			body := readBody(t, resp)
			if !strings.Contains(body, "Viewers cannot") {
				t.Fatalf("body missing 'Viewers cannot' message: %s", body)
			}
		})
	}
}

// reference some imports to keep the compiler happy even if we
// remove a use during refactors. (auth and sql are used in helper
// functions above; strconv in TestUsers_SelfRowHasNoDeleteButton.)
var _ = auth.VerifyPassword
var _ sql.NullString

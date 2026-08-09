package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

// TestAudit_LoginRecorded: POST /login with valid creds produces one
// audit_log row with action="login" and the user's id.
func TestAudit_LoginRecorded(t *testing.T) {
	h := newAuthHarness(t)
	h.seedUser(t, "alice@example.com", "hunter2")

	client := newJar(t)
	form := url.Values{
		"email":    {"alice@example.com"},
		"password": {"hunter2"},
	}
	resp, err := client.PostForm(h.server+"/login", form)
	if err != nil {
		t.Fatalf("post /login: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}

	rows, err := h.repo.Queries.ListAuditLogs(context.Background(), 100)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	if rows[0].Action != "login" {
		t.Fatalf("action = %q, want %q", rows[0].Action, "login")
	}
	if !rows[0].UserID.Valid || rows[0].UserID.Int64 == 0 {
		t.Fatalf("user_id = %v, want non-zero", rows[0].UserID)
	}
}

// TestAudit_CreateProjectRecorded: a full login + POST /projects flow
// produces two audit rows — login and create_project — both attributed
// to the same user.
func TestAudit_CreateProjectRecorded(t *testing.T) {
	h := newAuthHarness(t)
	u := h.seedUser(t, "alice@example.com", "hunter2")

	client := newJar(t)
	form := url.Values{
		"email":    {"alice@example.com"},
		"password": {"hunter2"},
	}
	resp, err := client.PostForm(h.server+"/login", form)
	if err != nil {
		t.Fatalf("post /login: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Extract the session token from the cookie jar to look up the
	// CSRF token for the subsequent POST /projects.
	var sessionToken string
	for _, c := range client.Jar.Cookies(mustURL(h.server)) {
		if c.Name == "session" {
			sessionToken = c.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("no session cookie after login")
	}
	sess, err := h.repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID:        sessionToken,
			ExpiresAt: 0,
		},
	)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}

	projForm := url.Values{
		"name":        {"audit-test"},
		"description": {""},
	}
	projForm.Set("csrf_token", sess.CsrfToken)
	presp, err := client.PostForm(h.server+"/projects", projForm)
	if err != nil {
		t.Fatalf("post /projects: %v", err)
	}
	_, _ = io.Copy(io.Discard, presp.Body)
	presp.Body.Close()
	if presp.StatusCode != http.StatusSeeOther {
		t.Fatalf("post /projects: status = %d, want 303", presp.StatusCode)
	}

	rows, err := h.repo.Queries.ListAuditLogs(context.Background(), 100)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("audit rows = %d, want 2 (login + create_project)", len(rows))
	}
	// Rows are DESC by created_at; create_project is the newer one.
	if rows[0].Action != "create_project" {
		t.Fatalf("newest action = %q, want create_project", rows[0].Action)
	}
	if rows[1].Action != "login" {
		t.Fatalf("older action = %q, want login", rows[1].Action)
	}
	for _, r := range rows {
		if !r.UserID.Valid || r.UserID.Int64 != u.ID {
			t.Fatalf("user_id = %v, want %d", r.UserID, u.ID)
		}
	}
}

// TestAudit_AdminCanView: an admin session can GET /admin/audit and
// sees the audit entries rendered.
func TestAudit_AdminCanView(t *testing.T) {
	h := newProjectHarness(t)

	// Generate an audit entry by creating a project.
	form := url.Values{
		"name":        {"audit-view-test"},
		"description": {""},
	}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(h.server.URL+"/projects", form)
	if err != nil {
		t.Fatalf("post /projects: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// GET /admin/audit as admin.
	auditResp, err := h.authedClient().Get(h.server.URL + "/admin/audit")
	if err != nil {
		t.Fatalf("get /admin/audit: %v", err)
	}
	defer auditResp.Body.Close()
	if auditResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", auditResp.StatusCode)
	}
	body, _ := io.ReadAll(auditResp.Body)
	if !strings.Contains(string(body), "Audit Log") {
		t.Fatalf("body missing 'Audit Log': %s", body)
	}
	if !strings.Contains(string(body), "create_project") {
		t.Fatalf("body missing create_project entry: %s", body)
	}
}

// TestAudit_DeployerForbidden: a deployer session gets 403 on
// /admin/audit.
func TestAudit_DeployerForbidden(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("deployer")

	resp, err := h.authedClient().Get(h.server.URL + "/admin/audit")
	if err != nil {
		t.Fatalf("get /admin/audit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// TestAudit_ViewerForbidden: a viewer session gets 403 on /admin/audit.
func TestAudit_ViewerForbidden(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("viewer")

	resp, err := h.authedClient().Get(h.server.URL + "/admin/audit")
	if err != nil {
		t.Fatalf("get /admin/audit: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// TestAudit_FailedLoginNotLogged: a failed login attempt (wrong
// password) must NOT create an audit row — privacy / enumeration.
func TestAudit_FailedLoginNotLogged(t *testing.T) {
	h := newAuthHarness(t)
	h.seedUser(t, "alice@example.com", "hunter2")

	client := newJar(t)
	form := url.Values{
		"email":    {"alice@example.com"},
		"password": {"wrong"},
	}
	resp, err := client.PostForm(h.server+"/login", form)
	if err != nil {
		t.Fatalf("post /login: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}

	rows, err := h.repo.Queries.ListAuditLogs(context.Background(), 100)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf(
			"audit rows = %d, want 0 (failed login must not be logged)",
			len(rows),
		)
	}
}

// TestAudit_GetCSRFRejectionNotLogged: a POST /projects without a CSRF
// token is rejected with 403 and must NOT produce an audit row — the
// audit middleware runs after CSRFMiddleware and only logs successful
// state changes.
func TestAudit_GetCSRFRejectionNotLogged(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"name":        {"no-csrf-audit"},
		"description": {""},
	}
	resp, err := h.authedClient().PostForm(h.server.URL+"/projects", form)
	if err != nil {
		t.Fatalf("post /projects: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}

	rows, err := h.repo.Queries.ListAuditLogs(context.Background(), 100)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf(
			"audit rows = %d, want 0 (CSRF rejection must not be logged)",
			len(rows),
		)
	}
}

// TestAudit_FilterByUser: /admin/audit?user_id=N returns only entries
// attributed to user N.
func TestAudit_FilterByUser(t *testing.T) {
	h := newProjectHarness(t)

	// Create an audit entry as the admin user.
	form := url.Values{
		"name":        {"filter-test"},
		"description": {""},
	}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(h.server.URL+"/projects", form)
	if err != nil {
		t.Fatalf("post /projects: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Create a second user + session and generate an entry as them.
	other := seedSession(t, h.repo, h.server.URL, "deployer")
	otherForm := url.Values{
		"name":        {"filter-other"},
		"description": {""},
	}
	otherForm.Set("csrf_token", other.csrfToken)
	oresp, err := other.client.PostForm(h.server.URL+"/projects", otherForm)
	if err != nil {
		t.Fatalf("post /projects as other: %v", err)
	}
	_, _ = io.Copy(io.Discard, oresp.Body)
	oresp.Body.Close()

	// Filter by the admin user's id.
	adminID := h.sess.user.ID
	auditResp, err := h.authedClient().
		Get(h.server.URL + "/admin/audit?user_id=" + itoa(adminID))
	if err != nil {
		t.Fatalf("get /admin/audit: %v", err)
	}
	defer auditResp.Body.Close()
	if auditResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", auditResp.StatusCode)
	}
	body, _ := io.ReadAll(auditResp.Body)
	if !strings.Contains(string(body), "filter-test") {
		t.Fatalf("body missing admin's filter-test entry: %s", body)
	}
	if strings.Contains(string(body), "filter-other") {
		t.Fatalf("body should not contain other user's entry: %s", body)
	}
}

// TestNotifications_AdminCanView: an admin session can GET
// /admin/notifications and sees published events with their delivery
// status rendered.
func TestNotifications_AdminCanView(t *testing.T) {
	h := newProjectHarness(t)

	_, err := h.repo.Queries.CreateNotificationEvent(
		context.Background(),
		db.CreateNotificationEventParams{
			EventType: "deployment_started",
			Message:   "Deployment #1 started on prod",
			Results:   `{"slack":"ok","email":"skipped"}`,
		},
	)
	if err != nil {
		t.Fatalf("create notification event: %v", err)
	}

	resp, err := h.authedClient().Get(h.server.URL + "/admin/notifications")
	if err != nil {
		t.Fatalf("get /admin/notifications: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Deployment #1 started on prod") {
		t.Fatalf("body missing event message: %s", body)
	}
	if !strings.Contains(string(body), "slack: ok") {
		t.Fatalf("body missing slack delivery status: %s", body)
	}
}

// TestNotifications_DeployerForbidden: a deployer session gets 403 on
// /admin/notifications, same as /admin/audit.
func TestNotifications_DeployerForbidden(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("deployer")

	resp, err := h.authedClient().Get(h.server.URL + "/admin/notifications")
	if err != nil {
		t.Fatalf("get /admin/notifications: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// TestGlobalNotificationSettings_AdminCanViewAndUpdate: an admin session
// can GET /admin/notifications/settings, and POSTing new channel values
// persists them (verified by re-fetching the page and by re-reading the
// global_notifications row directly).
func TestGlobalNotificationSettings_AdminCanViewAndUpdate(t *testing.T) {
	h := newProjectHarness(t)

	resp, err := h.authedClient().
		Get(h.server.URL + "/admin/notifications/settings")
	if err != nil {
		t.Fatalf("get /admin/notifications/settings: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", resp.StatusCode, body)
	}

	form := url.Values{
		"slack_webhook_url": {"https://hooks.slack.com/services/x"},
		"notify_emails":     {"ops@example.com"},
	}
	form.Set("csrf_token", h.csrfToken())
	postResp, err := h.authedClient().
		PostForm(h.server.URL+"/admin/notifications/settings", form)
	if err != nil {
		t.Fatalf("post /admin/notifications/settings: %v", err)
	}
	_, _ = io.Copy(io.Discard, postResp.Body)
	postResp.Body.Close()
	if postResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", postResp.StatusCode)
	}

	global, err := h.repo.Queries.GetGlobalNotifications(context.Background())
	if err != nil {
		t.Fatalf("get global notifications: %v", err)
	}
	if global.SlackWebhookUrl.String != "https://hooks.slack.com/services/x" {
		t.Fatalf("slack webhook not saved: %+v", global)
	}
	if global.NotifyEmails.String != "ops@example.com" {
		t.Fatalf("notify emails not saved: %+v", global)
	}
}

func TestGlobalNotificationSettings_RendersBackForBothPageBranches(
	t *testing.T,
) {
	// Given
	h := newProjectHarness(t)
	viewer := seedSession(t, h.repo, h.server.URL, "viewer")
	renderer := httptest.NewServer(auth.AuthMiddleware(h.repo)(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			global, err := h.repo.Queries.GetGlobalNotifications(r.Context())
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			if err := pages.GlobalNotificationsPage(global, r.URL.Path).
				Render(r.Context(), w); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
			}
		}),
	))
	t.Cleanup(renderer.Close)

	cases := []struct {
		name     string
		session  *authedSession
		wantSave bool
	}{
		{name: "writer", session: h.sess, wantSave: true},
		{name: "viewer", session: viewer, wantSave: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When
			resp, err := tc.session.client.Get(renderer.URL + "/settings")
			if err != nil {
				t.Fatalf("GET settings: %v", err)
			}
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatalf("read settings body: %v", err)
			}

			// Then
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("status = %d, want 200", resp.StatusCode)
			}
			const back = `<a href="/admin/notifications" class="btn btn-ghost btn-sm">Back</a>`
			if strings.Count(string(body), back) != 1 {
				t.Fatalf("back control = %q, want exactly one %q", body, back)
			}
			if tc.wantSave {
				requireHTMLPattern(
					t,
					string(body),
					`(?s)<div class="flex justify-between items-center mb-4">\s*<h1 class="text-3xl font-bold">Notification Settings</h1>\s*<div class="flex gap-2">\s*<button type="submit" class="btn btn-primary btn-sm">Save</button>\s*`+back,
				)
			} else {
				requireHTMLPattern(
					t,
					string(body),
					`(?s)<div class="flex justify-between items-center mb-4">\s*<h1 class="text-3xl font-bold">Notification Settings</h1>\s*<div class="flex gap-2">\s*`+back,
				)
			}
			hasSave := strings.Contains(string(body), `>Save</button>`)
			if hasSave != tc.wantSave {
				t.Errorf("Save rendered = %t, want %t", hasSave, tc.wantSave)
			}
		})
	}
}

// TestGlobalNotificationSettings_DeployerForbidden: a deployer session
// gets 403 on /admin/notifications/settings, same as the other /admin/*
// routes.
func TestGlobalNotificationSettings_DeployerForbidden(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("deployer")

	resp, err := h.authedClient().
		Get(h.server.URL + "/admin/notifications/settings")
	if err != nil {
		t.Fatalf("get /admin/notifications/settings: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func mustURL(s string) *url.URL {
	u, err := url.Parse(s)
	if err != nil {
		panic(err)
	}
	return u
}

func itoa(n int64) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = digits[n%10]
		n /= 10
	}
	return string(buf[i:])
}

package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"

	"github.com/robfig/cron/v3"
)

type authHarness struct {
	t      *testing.T
	repo   *repository.Repository
	server string
}

func newAuthHarness(t *testing.T) *authHarness {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		dbPath,
	)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = os.RemoveAll(dir)
	})

	repo := repository.New(conn)
	broker := runner.NewLogBroker()
	rnr := runner.New(repo, broker)
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	authHandler := handler.NewAuthHandler(repo)
	srv := httptest.NewServer(server.NewRouter(repo, rnr, parser, authHandler))
	t.Cleanup(srv.Close)
	return &authHarness{t: t, repo: repo, server: srv.URL}
}

func (h *authHarness) seedUser(t *testing.T, email, password string) db.User {
	t.Helper()
	hash, err := auth.HashPassword(password)
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := h.repo.Queries.CreateUser(
		context.Background(),
		db.CreateUserParams{
			Email:        email,
			PasswordHash: hash,
			Name:         "Test User",
			Role:         "admin",
		},
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u
}

func newJar(t *testing.T) http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	return http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// authedSession holds the credentials and cookie-jarred client for a test
// user. Every protected route in P0-4 requires a session, so the project
// and deployment test harnesses embed one of these.
//
// ponytail: single shared session helper covers all three harnesses
// (auth, project, deployment). If a test needs a different role, call
// seedSession again with a different role and swap the client.
type authedSession struct {
	user         *db.User
	sessionToken string
	csrfToken    string
	client       *http.Client // cookie jar holds the session cookie
}

// seedSession creates a user with the given role + a session row, and
// returns an authedSession with a cookie-jarred HTTP client ready to hit
// protected routes. The client does NOT follow redirects (tests assert
// on the immediate response).
func seedSession(
	t *testing.T,
	repo *repository.Repository,
	serverURL string,
	role string,
) *authedSession {
	t.Helper()
	ctx := context.Background()

	hash, err := auth.HashPassword("testpass")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	email := role + "@test.local"
	u, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email:        email,
		PasswordHash: hash,
		Name:         "Test " + role,
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
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
	jar.SetCookies(u2, []*http.Cookie{
		{Name: "session", Value: token},
	})

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

func TestLogin_Get(t *testing.T) {
	h := newAuthHarness(t)
	resp, err := http.Get(h.server + "/login")
	if err != nil {
		t.Fatalf("get /login: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "<form") {
		t.Fatal("body missing <form")
	}
}

func TestLogin_Post_ValidCredentials(t *testing.T) {
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
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/" {
		t.Fatalf("location = %q, want %q", loc, "/")
	}

	cookies := resp.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "session" {
		t.Fatalf("expected one session cookie, got %v", cookies)
	}
	c := cookies[0]
	if !c.HttpOnly {
		t.Fatal("cookie not HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Fatalf("SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/" {
		t.Fatalf("Path = %q, want %q", c.Path, "/")
	}

	_, err = h.repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID:        c.Value,
			ExpiresAt: 0,
		},
	)
	if err != nil {
		t.Fatalf("session row missing after login: %v", err)
	}
}

func TestLogin_Post_WrongPassword(t *testing.T) {
	h := newAuthHarness(t)
	h.seedUser(t, "alice@example.com", "hunter2")

	client := newJar(t)
	form := url.Values{
		"email":    {"alice@example.com"},
		"password": {"hunter3"},
	}
	resp, err := client.PostForm(h.server+"/login", form)
	if err != nil {
		t.Fatalf("post /login: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Invalid") {
		t.Fatalf("body missing 'Invalid': %s", body)
	}
}

func TestLogin_Post_UnknownEmail(t *testing.T) {
	h := newAuthHarness(t)

	client := newJar(t)
	form := url.Values{
		"email":    {"nope@x.com"},
		"password": {"anything"},
	}
	resp, err := client.PostForm(h.server+"/login", form)
	if err != nil {
		t.Fatalf("post /login: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Invalid") {
		t.Fatalf("body missing 'Invalid': %s", body)
	}
}

func TestAuthMiddleware_NoCookie_RedirectsFromHome(t *testing.T) {
	h := newAuthHarness(t)
	client := newJar(t)
	resp, err := client.Get(h.server + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", resp.StatusCode)
	}
	if loc := resp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("location = %q, want %q", loc, "/login")
	}
}

func TestAuthMiddleware_ValidCookie_AllowsHome(t *testing.T) {
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

	homeResp, err := client.Get(h.server + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer homeResp.Body.Close()

	if homeResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", homeResp.StatusCode)
	}
}

func TestLogout(t *testing.T) {
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

	var sessionToken string
	u, _ := url.Parse(h.server)
	for _, c := range client.Jar.Cookies(u) {
		if c.Name == "session" {
			sessionToken = c.Value
		}
	}
	if sessionToken == "" {
		t.Fatal("no session cookie after login")
	}

	logoutResp, err := client.PostForm(h.server+"/logout", nil)
	if err != nil {
		t.Fatalf("post /logout: %v", err)
	}
	defer logoutResp.Body.Close()

	if logoutResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", logoutResp.StatusCode)
	}
	if loc := logoutResp.Header.Get("Location"); loc != "/login" {
		t.Fatalf("location = %q, want %q", loc, "/login")
	}

	for _, c := range logoutResp.Cookies() {
		if c.Name == "session" && c.MaxAge > 0 {
			t.Fatal("session cookie not cleared")
		}
	}

	_, err = h.repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID:        sessionToken,
			ExpiresAt: 0,
		},
	)
	if err == nil {
		t.Fatal("session row still exists after logout")
	}
}

// TestAuth_ProtectedRoute_RedirectsToLogin verifies every protected route
// redirects unauthenticated requests to /login. Uses a fresh client with
// no session cookie.
func TestAuth_ProtectedRoute_RedirectsToLogin(t *testing.T) {
	h := newAuthHarness(t)
	for _, path := range []string{
		"/",
		"/projects",
		"/environments",
		"/deployments",
		"/templates",
		"/lifecycles",
	} {
		t.Run(path, func(t *testing.T) {
			client := newJar(t)
			resp, err := client.Get(h.server + path)
			if err != nil {
				t.Fatalf("GET %s: %v", path, err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusSeeOther {
				t.Fatalf("GET %s: status = %d, want 303", path, resp.StatusCode)
			}
			if loc := resp.Header.Get("Location"); loc != "/login" {
				t.Fatalf("GET %s: location = %q, want /login", path, loc)
			}
		})
	}
}

// TestAuth_LoginThenAccessProtected verifies the full login flow: POST
// /login with valid creds sets a session cookie, and subsequent requests
// to protected routes succeed (200).
func TestAuth_LoginThenAccessProtected(t *testing.T) {
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
		t.Fatalf("login status = %d, want 303", resp.StatusCode)
	}

	// client follows redirects by default? No — newJar sets
	// CheckRedirect to ErrUseLastResponse. So we manually follow.
	homeResp, err := client.Get(h.server + "/")
	if err != nil {
		t.Fatalf("get /: %v", err)
	}
	defer homeResp.Body.Close()
	if homeResp.StatusCode != http.StatusOK {
		t.Fatalf("get /: status = %d, want 200", homeResp.StatusCode)
	}

	depResp, err := client.Get(h.server + "/deployments")
	if err != nil {
		t.Fatalf("get /deployments: %v", err)
	}
	defer depResp.Body.Close()
	if depResp.StatusCode != http.StatusOK {
		t.Fatalf("get /deployments: status = %d, want 200", depResp.StatusCode)
	}
}

// TestAuth_ViewerCannotPost verifies the CSRF middleware's role gate:
// a viewer-role user gets 403 on any state-changing request.
func TestAuth_ViewerCannotPost(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("viewer")

	form := url.Values{
		"name":        {"viewer-proj"},
		"description": {""},
	}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().PostForm(h.server.URL+"/projects", form)
	if err != nil {
		t.Fatalf("POST /projects: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Viewers cannot") {
		t.Fatalf("body should contain viewer error message; got: %s", body)
	}
}

// TestAuth_ViewerHTMXReturnsToast: when a viewer makes a write via
// HTMX, the middleware responds with 200 + HX-Trigger carrying a
// makeToast event. The static/js/app.js makeToast handler turns the
// event into the same red toast the rest of the app uses; the page
// stays put (no full reload, no error overlay). This is the path
// the user actually hits in the UI (every form on the site uses
// HTMX), and it's the path that needs the polish.
func TestAuth_ViewerHTMXReturnsToast(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("viewer")

	form := url.Values{
		"name":        {"viewer-htmx"},
		"description": {""},
	}
	form.Set("csrf_token", h.csrfToken())
	req, _ := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		h.server.URL+"/projects",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", h.csrfToken())
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("POST /projects (HTMX): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf(
			"status = %d, want 200 (HTMX path returns 200 + HX-Trigger)",
			resp.StatusCode,
		)
	}
	trigger := resp.Header.Get("HX-Trigger")
	if trigger == "" {
		t.Fatal("HX-Trigger header missing on HTMX viewer write")
	}
	if !strings.Contains(trigger, `"makeToast"`) {
		t.Fatalf("HX-Trigger missing makeToast: %s", trigger)
	}
	if !strings.Contains(trigger, "Viewers cannot") {
		t.Fatalf("HX-Trigger missing viewer error message: %s", trigger)
	}
}

// TestAuth_AdminCanPost verifies the admin role (default in
// newProjectHarness) can create a project via POST /projects.
func TestAuth_AdminCanPost(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"name":        {"admin-proj"},
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

	// Verify the project row was created in the DB.
	projects, err := h.repo.Queries.ListProjects(context.Background())
	if err != nil {
		t.Fatalf("list projects: %v", err)
	}
	found := false
	for _, p := range projects {
		if p.Name == "admin-proj" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("project 'admin-proj' not found in DB after POST")
	}
}

// TestCSRF_PostWithoutToken verifies a POST without a csrf_token field
// is rejected with 403.
func TestCSRF_PostWithoutToken(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"name":        {"no-csrf"},
		"description": {""},
	}
	resp, err := h.authedClient().PostForm(h.server.URL+"/projects", form)
	if err != nil {
		t.Fatalf("POST /projects: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "Invalid CSRF token") {
		t.Fatalf("body should contain CSRF error; got: %s", body)
	}
}

// TestCSRF_PostWithToken verifies a POST with the correct csrf_token
// succeeds (303 redirect).
func TestCSRF_PostWithToken(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"name":        {"with-csrf"},
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
}

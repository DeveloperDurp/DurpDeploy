package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

// fullTokenRe matches a complete plaintext API token. The token list
// shows only the 12-char prefix, so this can only match the banner.
var fullTokenRe = regexp.MustCompile(`ddp_pat_[0-9a-f]{64}`)

// TestTokens_SettingsPageRenders: GET /settings/tokens as an admin
// renders the page with the create form and the empty-state message
// (no tokens yet).
func TestTokens_SettingsPageRenders(t *testing.T) {
	h := newProjectHarness(t)

	resp, err := h.authedClient().Get(h.server.URL + "/settings/tokens")
	if err != nil {
		t.Fatalf("GET /settings/tokens: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "API tokens") {
		t.Fatalf("body missing 'API tokens' heading: %s", body)
	}
	if !strings.Contains(body, `name="name"`) {
		t.Fatalf("body missing the name input: %s", body)
	}
	if !strings.Contains(body, "No tokens yet") {
		t.Fatalf("body missing empty-state: %s", body)
	}
}

// mintTokenViaForm posts the /settings/tokens create form and returns
// the Location header of the 303 redirect.
func mintTokenViaForm(t *testing.T, h *projectHarness, name string) string {
	t.Helper()
	form := url.Values{"name": {name}}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().
		PostForm(h.server.URL+"/settings/tokens", form)
	if err != nil {
		t.Fatalf("POST /settings/tokens: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create: status = %d, want 303", resp.StatusCode)
	}
	return resp.Header.Get("Location")
}

// secondSessionFor creates an additional session (own cookie jar) for
// an existing user, so tests can prove flash records are bound to the
// creating session and not merely to the user.
func secondSessionFor(
	t *testing.T,
	h *projectHarness,
	user *db.User,
) *authedSession {
	t.Helper()
	ctx := context.Background()
	token, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("new session token: %v", err)
	}
	if _, err := h.repo.Queries.CreateSession(ctx, db.CreateSessionParams{
		ID:        token,
		UserID:    user.ID,
		CsrfToken: csrf,
		ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar: %v", err)
	}
	u, err := url.Parse(h.server.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: "session", Value: token}})
	return &authedSession{
		user:         user,
		sessionToken: token,
		csrfToken:    csrf,
		client: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// getTokenBody fetches a URL with the given session and returns the
// body plus response.
func getTokenBody(
	t *testing.T,
	client *http.Client,
	baseURL, path string,
) (string, *http.Response) {
	t.Helper()
	resp, err := client.Get(baseURL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	return readBody(t, resp), resp
}

// TestTokens_CreateShowsBanner: POST /settings/tokens creates a token
// and redirects to an opaque single-use flash reference — never
// carrying the plaintext (issue #32). Following the redirect renders
// the one-time banner; replaying it shows nothing.
func TestTokens_CreateShowsBanner(t *testing.T) {
	h := newProjectHarness(t)

	loc := mintTokenViaForm(t, h, "ci-deploy")
	if !strings.HasPrefix(loc, "/settings/tokens?flash=") {
		t.Fatalf("redirect = %q, want /settings/tokens?flash=...", loc)
	}
	if strings.Contains(loc, "ddp_pat_") ||
		strings.Contains(loc, "new_token=") {
		t.Fatalf("redirect leaks the token: %q", loc)
	}

	// First display: the banner shows the plaintext token exactly once.
	body, resp := getTokenBody(t, h.authedClient(), h.server.URL, loc)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("display Cache-Control = %q, want no-store", got)
	}
	if !strings.Contains(body, "Token created") {
		t.Fatalf("body missing 'Token created' banner: %s", body)
	}
	if !strings.Contains(body, "ci-deploy") {
		t.Fatalf("body missing the token name: %s", body)
	}
	plaintext := fullTokenRe.FindString(body)
	if plaintext == "" {
		t.Fatalf("body missing the plaintext token: %s", body)
	}

	// Replay: the flash was consumed; no plaintext on the second GET.
	body2, _ := getTokenBody(t, h.authedClient(), h.server.URL, loc)
	if fullTokenRe.MatchString(body2) {
		t.Fatalf("flash replay showed the token again")
	}

	// The token row exists in the DB for the current user.
	rows, err := h.repo.Queries.ListApiTokensByUser(
		context.Background(), h.sess.user.ID,
	)
	if err != nil {
		t.Fatalf("list tokens: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("token rows = %d, want 1", len(rows))
	}
	if rows[0].Name != "ci-deploy" {
		t.Fatalf("token name = %q, want ci-deploy", rows[0].Name)
	}
}

// TestTokens_FlashBoundToSession: a flash record cannot be read from
// another session of the same user or by another user; only the
// creating session can consume it (issue #32).
func TestTokens_FlashBoundToSession(t *testing.T) {
	h := newProjectHarness(t)

	loc := mintTokenViaForm(t, h, "flash-bound")

	// Same user, different session: no token.
	second := secondSessionFor(t, h, h.sess.user)
	body, _ := getTokenBody(t, second.client, h.server.URL, loc)
	if fullTokenRe.MatchString(body) {
		t.Fatalf("flash readable from another session of the same user")
	}

	// Different user: no token (and the flash is not consumed).
	other := seedSessionAs(
		t, h.repo, h.server.URL, "flash-other@example.com", "deployer",
	)
	body, _ = getTokenBody(t, other.client, h.server.URL, loc)
	if fullTokenRe.MatchString(body) {
		t.Fatalf("flash readable by another user")
	}

	// The creating session still gets its one display.
	body, _ = getTokenBody(t, h.authedClient(), h.server.URL, loc)
	if !fullTokenRe.MatchString(body) {
		t.Fatalf("creating session lost its single display: %s", body)
	}
}

// TestTokens_FlashExpired: an expired flash record cannot be
// retrieved (issue #32).
func TestTokens_FlashExpired(t *testing.T) {
	h := newProjectHarness(t)

	flashID, _, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("new flash id: %v", err)
	}
	if _, err := h.repo.Queries.CreateTokenFlashSecret(
		context.Background(),
		db.CreateTokenFlashSecretParams{
			ID:         flashID,
			UserID:     h.sess.user.ID,
			SessionID:  h.sess.sessionToken,
			TokenValue: "ddp_pat_" + strings.Repeat("ab", 32),
			TokenName:  "old",
			ExpiresAt:  time.Now().Add(-time.Minute).Unix(),
		},
	); err != nil {
		t.Fatalf("seed expired flash: %v", err)
	}

	body, _ := getTokenBody(
		t, h.authedClient(), h.server.URL, "/settings/tokens?flash="+flashID,
	)
	if fullTokenRe.MatchString(body) {
		t.Fatalf("expired flash was displayed")
	}
}

// TestTokens_PageNotCacheable: /settings/tokens always sends
// Cache-Control: no-store and a restrictive Referrer-Policy (issue
// #32 — the page can render a plaintext credential).
func TestTokens_PageNotCacheable(t *testing.T) {
	h := newProjectHarness(t)

	body, resp := getTokenBody(
		t, h.authedClient(), h.server.URL, "/settings/tokens",
	)
	if got := resp.Header.Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
	if got := resp.Header.Get("Referrer-Policy"); got != "no-referrer" {
		t.Fatalf("Referrer-Policy = %q, want no-referrer", got)
	}
	if !strings.Contains(body, "API tokens") {
		t.Fatalf("unexpected page body: %s", body)
	}
}

// TestTokens_CreateMissingName: POST without a name 422s and
// re-renders the page with the error alert.
func TestTokens_CreateMissingName(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().
		PostForm(h.server.URL+"/settings/tokens", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Name is required") {
		t.Fatalf("body missing 'Name is required': %s", body)
	}
}

// TestTokens_SelfRevoke: POST /settings/tokens/{id}/revoke marks the
// user's own token as revoked; the list page then shows "Revoked".
func TestTokens_SelfRevoke(t *testing.T) {
	h := newProjectHarness(t)

	// Create a token via the web form.
	form := url.Values{"name": {"to-revoke"}}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().
		PostForm(h.server.URL+"/settings/tokens", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	rows, err := h.repo.Queries.ListApiTokensByUser(
		context.Background(), h.sess.user.ID,
	)
	if err != nil || len(rows) != 1 {
		t.Fatalf("expected 1 token, got rows=%v err=%v", rows, err)
	}
	tokenID := rows[0].ID

	// Revoke it.
	form2 := url.Values{}
	form2.Set("csrf_token", h.csrfToken())
	req, _ := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/settings/tokens/%s/revoke", h.server.URL, tokenID),
		strings.NewReader(form2.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", h.csrfToken())
	revokeResp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	_, _ = io.Copy(io.Discard, revokeResp.Body)
	revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke: status = %d, want 303", revokeResp.StatusCode)
	}

	// The list page shows the Revoked badge.
	listResp, err := h.authedClient().Get(h.server.URL + "/settings/tokens")
	if err != nil {
		t.Fatalf("GET list: %v", err)
	}
	defer listResp.Body.Close()
	body := readBody(t, listResp)
	if !strings.Contains(body, "Revoked") {
		t.Fatalf("list body missing 'Revoked' badge: %s", body)
	}
}

// TestTokens_SelfRevokeOtherUser404: a user cannot revoke another
// user's token via the self-service endpoint — the handler checks
// ownership and returns 404 (no existence leak).
func TestTokens_SelfRevokeOtherUser404(t *testing.T) {
	h := newProjectHarness(t)

	// Create a token as the admin (h.sess).
	form := url.Values{"name": {"admin-token"}}
	form.Set("csrf_token", h.csrfToken())
	resp, err := h.authedClient().
		PostForm(h.server.URL+"/settings/tokens", form)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	adminRows, err := h.repo.Queries.ListApiTokensByUser(
		context.Background(), h.sess.user.ID,
	)
	if err != nil || len(adminRows) != 1 {
		t.Fatalf("expected 1 admin token, got %v err=%v", adminRows, err)
	}
	tokenID := adminRows[0].ID

	// Switch to a deployer session.
	other := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"other@example.com",
		"deployer",
	)

	// The deployer tries to revoke the admin's token via the self path.
	form2 := url.Values{}
	form2.Set("csrf_token", other.csrfToken)
	req, _ := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/settings/tokens/%s/revoke", h.server.URL, tokenID),
		strings.NewReader(form2.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", other.csrfToken)
	revokeResp, err := other.client.Do(req)
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	defer revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusNotFound {
		t.Fatalf(
			"cross-user revoke: status = %d, want 404",
			revokeResp.StatusCode,
		)
	}

	// The token is still active.
	rows, err := h.repo.Queries.ListApiTokensByUser(
		context.Background(), h.sess.user.ID,
	)
	if err != nil || len(rows) != 1 {
		t.Fatalf(
			"admin token missing after cross-user revoke attempt: %v err=%v",
			rows,
			err,
		)
	}
	if rows[0].RevokedAt.Valid {
		t.Fatalf("token was revoked by a non-owner")
	}
}

// TestTokens_ViewerSeesForbidden: a viewer navigating to
// /settings/tokens sees the ViewerForbiddenMessage instead of the
// form. The CanWrite templ guard is the defensive layer (the CSRF
// middleware is the security boundary).
func TestTokens_ViewerSeesForbidden(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("viewer")

	resp, err := h.authedClient().Get(h.server.URL + "/settings/tokens")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := readBody(t, resp)
	if !strings.Contains(body, "Viewers cannot") {
		t.Fatalf("body missing 'Viewers cannot' message: %s", body)
	}
	if strings.Contains(body, `name="name"`) {
		t.Fatalf("viewer should not see the create form: %s", body)
	}
}

// TestTokens_ViewerNavHidden: the navbar "Tokens" link must NOT
// render for viewers (the inline viewer check in base.templ).
func TestTokens_ViewerNavHidden(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("viewer")

	resp, err := h.authedClient().Get(h.server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if strings.Contains(body, `href="/settings/tokens"`) {
		t.Fatalf("viewer nav should not contain Tokens link: %s", body)
	}
}

// TestTokens_AdminNavVisible: the navbar "Tokens" link renders for
// non-viewers (admin here).
func TestTokens_AdminNavVisible(t *testing.T) {
	h := newProjectHarness(t)

	resp, err := h.authedClient().Get(h.server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if !strings.Contains(body, `href="/settings/tokens"`) {
		t.Fatalf("admin nav should contain Tokens link: %s", body)
	}
}

// TestTokens_AdminDropdownLinks: the admin dropdown contains a link
// to /admin/tokens so admins can find the global token list.
func TestTokens_AdminDropdownLinks(t *testing.T) {
	h := newProjectHarness(t)

	resp, err := h.authedClient().Get(h.server.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	body := readBody(t, resp)
	if !strings.Contains(body, `href="/admin/tokens"`) {
		t.Fatalf("admin dropdown should link to /admin/tokens: %s", body)
	}
}

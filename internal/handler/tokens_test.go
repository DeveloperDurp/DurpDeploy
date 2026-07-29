package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"testing"
)

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

// TestTokens_CreateShowsBanner: POST /settings/tokens creates a token
// and the redirect's query string carries the plaintext; following
// the redirect renders the one-time banner with the token.
func TestTokens_CreateShowsBanner(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{"name": {"ci-deploy"}}
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
	loc := resp.Header.Get("Location")
	if !strings.HasPrefix(loc, "/settings/tokens?new_token=") {
		t.Fatalf("redirect = %q, want /settings/tokens?new_token=...", loc)
	}

	// Follow the redirect; the banner must show the plaintext token.
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
	if !strings.Contains(body, "Token created") {
		t.Fatalf("body missing 'Token created' banner: %s", body)
	}
	if !strings.Contains(body, "ci-deploy") {
		t.Fatalf("body missing the token name: %s", body)
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

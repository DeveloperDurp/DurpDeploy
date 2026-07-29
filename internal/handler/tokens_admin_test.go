package handler_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

// TestTokens_AdminPageListsAll: GET /admin/tokens as admin lists
// tokens from multiple users, with each owner's email shown.
func TestTokens_AdminPageListsAll(t *testing.T) {
	h := newProjectHarness(t)

	// Admin creates a token.
	form := url.Values{"name": {"admin-tok"}}
	form.Set("csrf_token", h.csrfToken())
	resp, _ := h.authedClient().PostForm(h.server.URL+"/settings/tokens", form)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// A second user creates a token.
	other := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"multi@example.com",
		"deployer",
	)
	form2 := url.Values{"name": {"multi-tok"}}
	form2.Set("csrf_token", other.csrfToken)
	resp2, _ := other.client.PostForm(h.server.URL+"/settings/tokens", form2)
	_, _ = io.Copy(io.Discard, resp2.Body)
	resp2.Body.Close()

	// Admin loads /admin/tokens.
	listResp, err := h.authedClient().Get(h.server.URL + "/admin/tokens")
	if err != nil {
		t.Fatalf("GET /admin/tokens: %v", err)
	}
	defer listResp.Body.Close()
	if listResp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", listResp.StatusCode)
	}
	body := readBody(t, listResp)
	if !strings.Contains(body, "admin-tok") {
		t.Fatalf("body missing admin-tok: %s", body)
	}
	if !strings.Contains(body, "multi-tok") {
		t.Fatalf("body missing multi-tok: %s", body)
	}
	if !strings.Contains(body, "multi@example.com") {
		t.Fatalf("body missing owner email: %s", body)
	}
}

// TestTokens_AdminRevoke: an admin can revoke any user's token via
// POST /admin/tokens/{id}/revoke.
func TestTokens_AdminRevoke(t *testing.T) {
	h := newProjectHarness(t)

	// A deployer creates a token.
	other := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"rev-me@example.com",
		"deployer",
	)
	form := url.Values{"name": {"rev-tok"}}
	form.Set("csrf_token", other.csrfToken)
	resp, _ := other.client.PostForm(h.server.URL+"/settings/tokens", form)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	otherRows, err := h.repo.Queries.ListApiTokensByUser(
		context.Background(), other.user.ID,
	)
	if err != nil || len(otherRows) != 1 {
		t.Fatalf(
			"expected 1 token for other user, got %v err=%v",
			otherRows,
			err,
		)
	}
	tokenID := otherRows[0].ID

	// Admin revokes it.
	form2 := url.Values{}
	form2.Set("csrf_token", h.csrfToken())
	req, _ := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/admin/tokens/%s/revoke", h.server.URL, tokenID),
		strings.NewReader(form2.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", h.csrfToken())
	revokeResp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("admin revoke: %v", err)
	}
	_, _ = io.Copy(io.Discard, revokeResp.Body)
	revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusSeeOther {
		t.Fatalf("admin revoke: status = %d, want 303", revokeResp.StatusCode)
	}

	// The token is revoked in the DB.
	allRows, err := h.repo.Queries.ListAllApiTokens(context.Background())
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	var found bool
	for _, r := range allRows {
		if r.ID == tokenID {
			found = true
			if !r.RevokedAt.Valid {
				t.Fatalf("token %s not revoked after admin revoke", tokenID)
			}
		}
	}
	if !found {
		t.Fatalf("token %s missing from ListAllApiTokens", tokenID)
	}
}

// TestTokens_NonAdminCannotAccessAdminPage: a deployer or viewer
// hitting /admin/tokens gets 403 from the RequireRole("admin")
// middleware.
func TestTokens_NonAdminCannotAccessAdminPage(t *testing.T) {
	for _, role := range []string{"deployer", "viewer"} {
		t.Run(role, func(t *testing.T) {
			h := newProjectHarness(t)
			h.setRole(role)

			resp, err := h.authedClient().Get(h.server.URL + "/admin/tokens")
			if err != nil {
				t.Fatalf("GET /admin/tokens: %v", err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", resp.StatusCode)
			}
		})
	}
}

// TestTokens_AuditCreateAndRevoke: the audit log records
// create_api_token and revoke_api_token for the web paths.
func TestTokens_AuditCreateAndRevoke(t *testing.T) {
	h := newProjectHarness(t)

	// Create.
	form := url.Values{"name": {"audit-tok"}}
	form.Set("csrf_token", h.csrfToken())
	resp, _ := h.authedClient().PostForm(h.server.URL+"/settings/tokens", form)
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	createEntries, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 50,
			FAction:   sql.NullString{String: "create_api_token", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(createEntries) != 1 {
		t.Fatalf("create_api_token audit rows = %d, want 1", len(createEntries))
	}

	// Revoke via self path.
	rows, err := h.repo.Queries.ListApiTokensByUser(
		context.Background(), h.sess.user.ID,
	)
	if err != nil || len(rows) != 1 {
		t.Fatalf("expected 1 token, got %v err=%v", rows, err)
	}
	form2 := url.Values{}
	form2.Set("csrf_token", h.csrfToken())
	req, _ := http.NewRequest(
		http.MethodPost,
		fmt.Sprintf("%s/settings/tokens/%s/revoke", h.server.URL, rows[0].ID),
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

	revokeEntries, err := h.repo.Queries.ListAuditLogsFiltered(
		context.Background(),
		db.ListAuditLogsFilteredParams{
			PageLimit: 50,
			FAction:   sql.NullString{String: "revoke_api_token", Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("list audit: %v", err)
	}
	if len(revokeEntries) != 1 {
		t.Fatalf("revoke_api_token audit rows = %d, want 1", len(revokeEntries))
	}
}

package handler_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func beginStaleAdminMFAReset(
	t *testing.T,
	h *authHarness,
	reset adminMFAResetTarget,
) *http.Response {
	t.Helper()
	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{ID: reset.Admin.sessionToken},
	); err != nil {
		t.Fatalf("make admin session stale: %v", err)
	}
	response := postAdminMFAReset(t, h, reset)
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/settings/security/reauth" {
		response.Body.Close()
		t.Fatalf(
			"stale reset response = %d %q, want reauth redirect",
			response.StatusCode,
			response.Header.Get("Location"),
		)
	}
	return response
}

func postPasswordReauthentication(
	t *testing.T,
	h *authHarness,
	admin *authedSession,
	password string,
	includeCSRF bool,
) *http.Response {
	t.Helper()
	values := url.Values{"password": {password}}
	if includeCSRF {
		values.Set("csrf_token", admin.csrfToken)
	}
	response, err := admin.client.PostForm(
		h.server+"/settings/security/reauth",
		values,
	)
	if err != nil {
		t.Fatalf("POST password reauthentication: %v", err)
	}
	return response
}

func assertNoAdminMFAReset(t *testing.T, h *authHarness, targetID int64) {
	t.Helper()
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		targetID,
	) != 1 {
		t.Fatal("target MFA was reset")
	}
	var auditCount int
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM audit_log",
	).Scan(&auditCount); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	if auditCount != 0 {
		t.Fatalf("audit rows = %d, want 0", auditCount)
	}
}

func alterAdminMFAResetContinuation(
	t *testing.T,
	h *authHarness,
	admin *authedSession,
	ceremony string,
) {
	t.Helper()
	result, err := h.repo.DB.ExecContext(
		context.Background(),
		`UPDATE mfa_challenges SET ceremony_json = ?
WHERE user_id = ? AND session_id = ? AND purpose = ?`,
		ceremony,
		admin.user.ID,
		admin.sessionToken,
		string(mfa.ChallengePurposeAdminMFAReset),
	)
	if err != nil {
		t.Fatalf("alter reset continuation: %v", err)
	}
	updated, err := result.RowsAffected()
	if err != nil || updated != 1 {
		t.Fatalf("altered continuations = %d, err = %v", updated, err)
	}
}

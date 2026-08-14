package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

var manualTOTPKey = regexp.MustCompile(
	`id="totp-manual-key"[^>]*>([^<]+)<`,
)

func TestSecurity_TOTPEnrollmentRendersRecoveryCodesAfterOneSubmission(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")

	// When
	begin := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/totp/begin",
		url.Values{},
		h.authHandler.SecurityTOTPBeginPost,
	)
	challenge := securityHiddenValues(t, begin.Body.String())
	manualKey := manualTOTPKey.FindStringSubmatch(begin.Body.String())
	if len(manualKey) != 2 {
		t.Fatal("TOTP enrollment did not display a one-time manual key")
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		current.user.ID,
	) != 0 {
		t.Fatal("unconfirmed TOTP enrollment enabled MFA")
	}
	code, err := mfa.GenerateTOTPCode(manualKey[1], time.Now())
	if err != nil {
		t.Fatalf("generate enrollment code: %v", err)
	}
	var ceremony string
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT ceremony_json FROM mfa_challenges WHERE user_id = ?",
		current.user.ID,
	).Scan(&ceremony); err != nil {
		t.Fatalf("read enrollment challenge: %v", err)
	}
	challenge.Set("code", code)
	confirm := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/totp/confirm",
		challenge,
		h.authHandler.SecurityTOTPConfirmPost,
	)

	// Then
	if begin.Code != http.StatusOK ||
		begin.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"TOTP begin response = %d %q",
			begin.Code,
			begin.Header().Get("Cache-Control"),
		)
	}
	if confirm.Code != http.StatusOK ||
		confirm.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"TOTP confirmation response = %d %q",
			confirm.Code,
			confirm.Header().Get("Cache-Control"),
		)
	}
	if strings.Count(confirm.Body.String(), `class="recovery-code `) != 10 ||
		strings.Contains(confirm.Body.String(), manualKey[1]) {
		t.Fatal("TOTP confirmation did not render committed recovery codes")
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		current.user.ID,
	) != 1 || countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
		current.user.ID,
	) != 10 || countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		current.user.ID,
	) != 0 {
		t.Fatal(
			"TOTP confirmation did not atomically activate and consume its challenge",
		)
	}
	if begin.Header().Get("Location") != "" ||
		confirm.Header().Get("Location") != "" {
		t.Fatal("TOTP lifecycle leaked a secret through a query redirect")
	}
	if strings.Contains(ceremony, manualKey[1]) {
		t.Fatal("pending enrollment seed was stored in plaintext")
	}
}

func TestSecurity_TOTPEnrollmentRemovesVerificationRoute(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")

	// When
	response := postSecurityValues(
		t,
		current,
		h.server,
		"/settings/security/totp/verify",
		url.Values{"csrf_token": {current.csrfToken}},
	)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf(
			"retired verification route status = %d, want 404",
			response.StatusCode,
		)
	}
}

func TestSecurity_TOTPBeginRedirectsWhenSessionIsStale(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{
			ID: current.sessionToken,
			ReauthenticatedAt: sql.NullInt64{
				Int64: time.Now().Add(-6 * time.Minute).Unix(),
				Valid: true,
			},
		},
	); err != nil {
		t.Fatalf("make session stale: %v", err)
	}

	// When
	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/totp/begin",
		url.Values{},
		h.authHandler.SecurityTOTPBeginPost,
	)

	// Then
	if response.Code != http.StatusSeeOther ||
		response.Header().Get("Location") != "/settings/security/reauth" {
		t.Fatalf("stale enrollment response = %d %q", response.Code,
			response.Header().Get("Location"))
	}
}

func TestSecurity_TOTPBeginRejectsConfiguredAuthenticator(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	seedTOTP(t, h, *current.user, box)

	// When
	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/totp/begin",
		url.Values{},
		h.authHandler.SecurityTOTPBeginPost,
	)

	// Then
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(
			response.Body.String(),
			"Unable to update security settings",
		) {
		t.Fatalf("configured TOTP begin response = %d %q", response.Code,
			response.Body.String())
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		current.user.ID,
	) != 0 {
		t.Fatal("configured TOTP begin created a challenge")
	}
}

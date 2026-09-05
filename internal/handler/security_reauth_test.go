package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestSecurity_ReauthUpdatesOnlyCurrentSessionAfterPasswordOnlyEnrollment(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	other := seedSecuritySession(t, h, current.user)
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
		t.Fatalf("make current session stale: %v", err)
	}
	otherBefore := readSession(t, h, other.sessionToken).ReauthenticatedAt

	// When
	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{"password": {"testpass"}},
		h.authHandler.SecurityReauthPost,
	)

	// Then
	if response.Code != http.StatusSeeOther ||
		response.Header().Get("Location") != "/settings/security" {
		t.Fatalf("password-only reauth response = %d %q", response.Code,
			response.Header().Get("Location"))
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("password-only reauth response is cacheable")
	}
	if !readSession(t, h, current.sessionToken).ReauthenticatedAt.Valid {
		t.Fatal("current session was not refreshed")
	}
	if after := readSession(
		t,
		h,
		other.sessionToken,
	).ReauthenticatedAt; after != otherBefore {
		t.Fatal("reauthentication updated another browser session")
	}
}

func TestSecurity_ReauthRequiresCurrentFactorWhenMFAEnabled(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	code := seedTOTP(t, h, *current.user, box)
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
	passwordResponse := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{"password": {"testpass"}},
		h.authHandler.SecurityReauthPost,
	)
	challenge := securityHiddenValues(t, passwordResponse.Body.String())
	challenge.Set("code", code)
	factorResponse := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth/totp",
		challenge,
		h.authHandler.SecurityReauthTOTPPost,
	)

	// Then
	if passwordResponse.Code != http.StatusOK ||
		!strings.Contains(
			passwordResponse.Body.String(),
			"Authenticator code",
		) {
		t.Fatal("password step did not require an alternative factor")
	}
	if factorResponse.Code != http.StatusSeeOther ||
		factorResponse.Header().Get("Location") != "/settings/security" {
		t.Fatalf("factor reauth response = %d %q", factorResponse.Code,
			factorResponse.Header().Get("Location"))
	}
}

func TestSecurity_ReauthRejectsWrongPasswordWithoutRefreshingSession(
	t *testing.T,
) {
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
	before := readSession(t, h, current.sessionToken).ReauthenticatedAt

	// When
	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{"password": {"wrong"}},
		h.authHandler.SecurityReauthPost,
	)

	// Then
	if response.Code != http.StatusUnprocessableEntity ||
		readSession(t, h, current.sessionToken).ReauthenticatedAt != before {
		t.Fatal("wrong password refreshed the current browser session")
	}
}

func TestSecurity_ReauthRejectsDummyPasswordForPasswordlessAccount(
	t *testing.T,
) {
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	if err := h.repo.Queries.UpdateUserPassword(
		context.Background(),
		db.UpdateUserPasswordParams{ID: current.user.ID, PasswordHash: ""},
	); err != nil {
		t.Fatalf("clear password: %v", err)
	}

	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{
			"password": {"durpdeploy unknown account timing placeholder"},
		},
		h.authHandler.SecurityReauthPost,
	)

	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", response.Code)
	}
}

func TestSecurity_ReauthConsumesRecoveryCode(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	seedTOTP(t, h, *current.user, box)
	const recoveryCode = "0123-4567-89AB-CDEF-0123-4567-89AB-CDEF"
	hash, err := mfa.HashRecoveryCode(recoveryCode)
	if err != nil {
		t.Fatalf("hash recovery code: %v", err)
	}
	if _, err := h.repo.Queries.CreateRecoveryCode(
		context.Background(),
		db.CreateRecoveryCodeParams{
			ID: "reauth-recovery", UserID: current.user.ID, CodeHash: hash[:],
		},
	); err != nil {
		t.Fatalf("create recovery code: %v", err)
	}

	// When
	password := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{"password": {"testpass"}},
		h.authHandler.SecurityReauthPost,
	)
	challenge := securityHiddenValues(t, password.Body.String())
	challenge.Set("code", recoveryCode)
	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth/recovery",
		challenge,
		h.authHandler.SecurityReauthRecoveryPost,
	)

	// Then
	if response.Code != http.StatusSeeOther ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ? AND used_at IS NULL",
			current.user.ID,
		) != 0 {
		t.Fatal("recovery-code reauthentication did not consume the code")
	}
}

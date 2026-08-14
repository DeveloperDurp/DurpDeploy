package handler_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestSecurity_PasskeyRenameAndDeleteRejectLastFactor(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(current.user.ID, []byte("first")),
	); err != nil {
		t.Fatalf("create first passkey: %v", err)
	}
	second := securityCredentialParams(current.user.ID, []byte("second"))
	second.Name = "secondary"
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(), second,
	); err != nil {
		t.Fatalf("create second passkey: %v", err)
	}

	// When
	rename := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/passkeys/first/rename",
		url.Values{
			"credential_id": {
				base64.RawURLEncoding.EncodeToString([]byte("first")),
			},
			"name": {"Desk key"},
		},
		h.authHandler.SecurityPasskeyRenamePost,
	)
	current = seedSecuritySession(t, h, current.user)
	deleteFirst := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/passkeys/second/delete",
		url.Values{"credential_id": {
			base64.RawURLEncoding.EncodeToString([]byte("second")),
		}},
		h.authHandler.SecurityPasskeyDeletePost,
	)
	current = seedSecuritySession(t, h, current.user)
	deleteLast := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/passkeys/first/delete",
		url.Values{"credential_id": {
			base64.RawURLEncoding.EncodeToString([]byte("first")),
		}},
		h.authHandler.SecurityPasskeyDeletePost,
	)

	// Then
	if rename.Code != http.StatusSeeOther ||
		deleteFirst.Code != http.StatusSeeOther {
		t.Fatalf(
			"passkey mutation statuses = %d, %d",
			rename.Code,
			deleteFirst.Code,
		)
	}
	credential, err := h.repo.Queries.GetWebAuthnCredentialByID(
		context.Background(),
		[]byte("first"),
	)
	if err != nil || credential.Name != "Desk key" {
		t.Fatal("passkey rename did not update the owned credential")
	}
	if deleteLast.Code != http.StatusUnprocessableEntity ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?",
			current.user.ID,
		) != 1 {
		t.Fatal("individual passkey deletion removed the last factor")
	}
}

func TestSecurity_RegenerateRecoveryAndDisableInvalidateBrowserState(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	code := seedTOTP(t, h, *current.user, box)
	recoveryHash, err := mfa.HashRecoveryCode(
		"0123-4567-89AB-CDEF-0123-4567-89AB-CDEF",
	)
	if err != nil {
		t.Fatalf("hash recovery code: %v", err)
	}
	if _, err := h.repo.Queries.CreateRecoveryCode(
		context.Background(),
		db.CreateRecoveryCodeParams{
			ID: "security-recovery", UserID: current.user.ID, CodeHash: recoveryHash[:],
		},
	); err != nil {
		t.Fatalf("create recovery code: %v", err)
	}
	current = seedSecuritySession(t, h, current.user)
	password := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth",
		url.Values{"password": {"testpass"}},
		h.authHandler.SecurityReauthPost,
	)
	challenge := securityHiddenValues(t, password.Body.String())
	challenge.Set("code", code)
	_ = postSecurityForm(
		t,
		h,
		current,
		"/settings/security/reauth/totp",
		challenge,
		h.authHandler.SecurityReauthTOTPPost,
	)
	regen := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/recovery/regenerate",
		url.Values{},
		h.authHandler.SecurityRecoveryRegeneratePost,
	)
	current = seedSecuritySession(t, h, current.user)
	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{
			ID:                current.sessionToken,
			ReauthenticatedAt: sql.NullInt64{Int64: 1, Valid: true},
		},
	); err != nil {
		t.Fatalf("make disable session stale: %v", err)
	}
	staleDisable := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/disable",
		url.Values{},
		h.authHandler.SecurityDisablePost,
	)

	// Then
	if regen.Code != http.StatusOK ||
		regen.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(
			"recovery regeneration did not return a no-store one-time response",
		)
	}
	var oldRecoveryCode int
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ? AND code_hash = ?",
		current.user.ID,
		recoveryHash[:],
	).Scan(&oldRecoveryCode); err != nil {
		t.Fatalf("check replaced recovery code: %v", err)
	}
	if oldRecoveryCode != 0 {
		t.Fatal("recovery regeneration retained the previous code hash")
	}
	if staleDisable.Code != http.StatusSeeOther ||
		staleDisable.Header().Get("Location") != "/settings/security/reauth" {
		t.Fatal("stale session disabled MFA")
	}

	// Given a current-factor reauthenticated session.
	if _, err := h.repo.DB.ExecContext(
		context.Background(),
		"UPDATE mfa_totp SET last_accepted_step = NULL WHERE user_id = ?",
		current.user.ID,
	); err != nil {
		t.Fatalf("reset TOTP test step: %v", err)
	}
	current = seedSecuritySession(t, h, current.user)
	code, err = mfa.GenerateTOTPCode("JBSWY3DPEHPK3PXP", time.Now())
	if err != nil {
		t.Fatalf("generate reauthentication code: %v", err)
	}
	password = postSecurityForm(t, h, current, "/settings/security/reauth",
		url.Values{"password": {"testpass"}}, h.authHandler.SecurityReauthPost)
	challenge = securityHiddenValues(t, password.Body.String())
	challenge.Set("code", code)
	_ = postSecurityForm(t, h, current, "/settings/security/reauth/totp",
		challenge, h.authHandler.SecurityReauthTOTPPost)
	apiToken, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint API token: %v", err)
	}
	if _, err := h.repo.Queries.CreateApiToken(
		context.Background(),
		db.CreateApiTokenParams{
			ID: "security-disable-token", UserID: current.user.ID, Name: "preserved",
			TokenPrefix: prefix, TokenHash: hash, Scope: "global",
		},
	); err != nil {
		t.Fatalf("create API token: %v", err)
	}
	if _, err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{
		Repository: h.repo,
	}).Issue(context.Background(), mfa.ChallengeIssue{
		UserID: current.user.ID, SessionID: current.sessionToken,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	}); err != nil {
		t.Fatalf("create pending MFA challenge: %v", err)
	}

	// When
	disable := postSecurityForm(t, h, current, "/settings/security/disable",
		url.Values{}, h.authHandler.SecurityDisablePost)

	// Then
	if disable.Code != http.StatusSeeOther ||
		disable.Header().Get("Location") != "/login" {
		t.Fatalf(
			"disable response = %d %q",
			disable.Code,
			disable.Header().Get("Location"),
		)
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		current.user.ID,
	) != 0 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
			current.user.ID,
		) != 0 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
			current.user.ID,
		) != 0 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
			current.user.ID,
		) != 0 {
		t.Fatal("disable did not atomically remove MFA browser state")
	}
	if apiToken == "" ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM api_tokens WHERE user_id = ?",
			current.user.ID,
		) != 1 {
		t.Fatal("disable changed API token state")
	}
}

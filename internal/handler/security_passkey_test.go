package handler_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

type passkeyBeginResponse struct {
	CSRF    string          `json:"csrf"`
	Options json.RawMessage `json:"options"`
	Token   string          `json:"token"`
}

func TestSecurity_PasskeyRegistrationUsesFreshSession(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
		Enabled: true,
		Origin:  "https://example.org",
		RPID:    "example.org",
	}})
	current := seedSession(t, h.repo, h.server, "admin")

	// When
	begin := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/passkeys/begin",
		url.Values{"name": {"Laptop"}},
		h.authHandler.SecurityPasskeyBeginPost,
	)
	var payload passkeyBeginResponse
	if err := json.Unmarshal(begin.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode passkey begin response: %v", err)
	}
	finish := postSecurityJSON(
		t,
		h,
		current,
		"/settings/security/passkeys/finish",
		"{}",
		h.authHandler.SecurityPasskeyFinishPost,
		payload.Token,
		payload.CSRF,
	)

	// Then
	if begin.Code != http.StatusOK ||
		begin.Header().Get("Cache-Control") != "no-store" ||
		payload.Token == "" ||
		payload.CSRF == "" ||
		len(payload.Options) == 0 {
		t.Fatal("passkey begin did not return a no-store bound ceremony")
	}
	if finish.Code != http.StatusUnprocessableEntity {
		t.Fatalf("malformed passkey finish status = %d, want 422", finish.Code)
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?",
		current.user.ID,
	) != 0 {
		t.Fatal("rejected passkey registration created a credential")
	}
}

func TestSecurity_PasskeyBeginRejectsConfiguredPasskey(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
		Enabled: true,
		Origin:  "https://example.org",
		RPID:    "example.org",
	}})
	current := seedSession(t, h.repo, h.server, "admin")
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(current.user.ID, []byte("configured-passkey")),
	); err != nil {
		t.Fatalf("create passkey: %v", err)
	}

	// When
	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/passkeys/begin",
		url.Values{"name": {"Laptop"}},
		h.authHandler.SecurityPasskeyBeginPost,
	)

	// Then
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(
			response.Body.String(),
			"Unable to update security settings",
		) {
		t.Fatalf("configured passkey begin response = %d %q", response.Code,
			response.Body.String())
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		current.user.ID,
	) != 0 {
		t.Fatal("configured passkey begin created a challenge")
	}
}

func TestSecurity_PasskeyBeginRedirectsWhenSessionIsStale(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
		Enabled: true,
		Origin:  "https://example.org",
		RPID:    "example.org",
	}})
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
		"/settings/security/passkeys/begin",
		url.Values{"name": {"Laptop"}},
		h.authHandler.SecurityPasskeyBeginPost,
	)

	// Then
	if response.Code != http.StatusSeeOther ||
		response.Header().Get("Location") != "/settings/security/reauth" {
		t.Fatalf("stale passkey begin response = %d %q", response.Code,
			response.Header().Get("Location"))
	}
}

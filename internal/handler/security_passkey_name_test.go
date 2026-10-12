package handler_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestSecurity_PasskeyNameRejectsOverlength(t *testing.T) {
	for _, name := range []string{
		strings.Repeat("a", 256), strings.Repeat("🔑", 128),
	} {
		t.Run(name[:4], func(t *testing.T) {
			// Given: a freshly authenticated user without a passkey.
			h := newAuthHarness(t)
			configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
				Enabled: true, Origin: "https://example.org", RPID: "example.org",
			}})
			current := seedSession(t, h.repo, h.server, "admin")

			// When: registration receives a name exceeding NVARCHAR(255).
			response := postSecurityForm(t, h, current,
				"/settings/security/passkeys/begin", url.Values{"name": {name}},
				h.authHandler.SecurityPasskeyBeginPost)

			// Then: validation rejects it before a ceremony is persisted.
			if response.Code != http.StatusUnprocessableEntity {
				t.Fatalf("begin status = %d, want 422", response.Code)
			}
			if countSecurityRows(t, h,
				"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
				current.user.ID) != 0 {
				t.Fatal("invalid passkey name created a challenge")
			}
		})
	}
}

func TestSecurity_PasskeyRenameRejectsOverlength(t *testing.T) {
	// Given: a user with a persisted passkey and a fresh browser session.
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	params := securityCredentialParams(current.user.ID, []byte("first"))
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(), params,
	); err != nil {
		t.Fatal(err)
	}

	// When: rename receives too many UTF-16 code units.
	response := postSecurityForm(t, h, current,
		"/settings/security/passkeys/rename", url.Values{
			"credential_id": {
				base64.RawURLEncoding.EncodeToString(params.CredentialID),
			},
			"name": {strings.Repeat("🔑", 128)},
		}, h.authHandler.SecurityPasskeyRenamePost)

	// Then: the existing credential and session remain unchanged.
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("rename status = %d, want 422", response.Code)
	}
	row, err := h.repo.Queries.GetWebAuthnCredentialByID(
		context.Background(), params.CredentialID)
	if err != nil || row.Name != params.Name {
		t.Fatalf("credential changed: name=%q, error=%v", row.Name, err)
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		current.user.ID,
	) != 1 {
		t.Fatal("invalid rename revoked the browser session")
	}
}

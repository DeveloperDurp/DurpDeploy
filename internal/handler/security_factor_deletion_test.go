package handler_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

const (
	invalidPasskeyDeleteMessage = "This passkey request is invalid. Refresh Security and try again."
	missingPasskeyDeleteMessage = "This passkey is unavailable. Refresh Security and try again."
	lastPasskeyDeleteMessage    = "Keep another factor configured before deleting this passkey."
)

func TestSecurity_PasskeyDeleteKeepsLastFactor(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSecuritySession(
		t,
		h,
		seedSession(t, h.repo, h.server, "admin").user,
	)
	credentialID := []byte("only-passkey")
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(current.user.ID, credentialID),
	); err != nil {
		t.Fatalf("create only passkey: %v", err)
	}

	// When
	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/passkeys/delete",
		url.Values{"credential_id": {
			base64.RawURLEncoding.EncodeToString(credentialID),
		}},
		h.authHandler.SecurityPasskeyDeletePost,
	)

	// Then
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), lastPasskeyDeleteMessage) ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?",
			current.user.ID,
		) != 1 {
		t.Fatal("last passkey deletion did not preserve the existing rejection")
	}
}

func TestSecurity_PasskeyDeleteRejectsMalformedOrEmptyCredential(t *testing.T) {
	for _, test := range []struct {
		name         string
		credentialID string
	}{
		{name: "empty", credentialID: ""},
		{name: "malformed", credentialID: "%"},
	} {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			configureMFA(t, h, mfa.Config{})
			current := seedSecuritySession(
				t,
				h,
				seedSession(t, h.repo, h.server, "admin").user,
			)

			// When
			response := postSecurityForm(
				t,
				h,
				current,
				"/settings/security/passkeys/delete",
				url.Values{"credential_id": {test.credentialID}},
				h.authHandler.SecurityPasskeyDeletePost,
			)

			// Then
			if response.Code != http.StatusUnprocessableEntity ||
				!strings.Contains(
					response.Body.String(),
					invalidPasskeyDeleteMessage,
				) {
				t.Fatalf(
					"invalid credential response = %d %q",
					response.Code,
					response.Body.String(),
				)
			}
		})
	}
}

func TestSecurity_PasskeyDeleteRejectsUnknownCredentialBeforeLastFactor(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSecuritySession(
		t,
		h,
		seedSession(t, h.repo, h.server, "admin").user,
	)
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(current.user.ID, []byte("owned")),
	); err != nil {
		t.Fatalf("create owned passkey: %v", err)
	}

	// When
	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/passkeys/delete",
		url.Values{"credential_id": {
			base64.RawURLEncoding.EncodeToString([]byte("unknown")),
		}},
		h.authHandler.SecurityPasskeyDeletePost,
	)

	// Then
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), missingPasskeyDeleteMessage) {
		t.Fatalf(
			"unknown credential response = %d %q",
			response.Code,
			response.Body.String(),
		)
	}
}

func TestSecurity_PasskeyDeleteDoesNotRevealForeignCredentialOwnership(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSecuritySession(
		t,
		h,
		seedSession(t, h.repo, h.server, "admin").user,
	)
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(current.user.ID, []byte("owned")),
	); err != nil {
		t.Fatalf("create owned passkey: %v", err)
	}
	other, err := h.repo.Queries.CreateUser(
		context.Background(),
		db.CreateUserParams{
			Email: "other@example.com", PasswordHash: "hash", Name: "Other", Role: "admin",
		},
	)
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	foreignCredentialID := []byte("foreign")
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(other.ID, foreignCredentialID),
	); err != nil {
		t.Fatalf("create foreign passkey: %v", err)
	}

	// When
	response := postSecurityForm(
		t,
		h,
		current,
		"/settings/security/passkeys/delete",
		url.Values{"credential_id": {
			base64.RawURLEncoding.EncodeToString(foreignCredentialID),
		}},
		h.authHandler.SecurityPasskeyDeletePost,
	)

	// Then
	if response.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(response.Body.String(), missingPasskeyDeleteMessage) {
		t.Fatalf(
			"foreign credential response = %d %q",
			response.Code,
			response.Body.String(),
		)
	}
	credential, err := h.repo.Queries.GetWebAuthnCredentialByID(
		context.Background(),
		foreignCredentialID,
	)
	if err != nil || credential.UserID != other.ID {
		t.Fatal("foreign passkey deletion changed another user's credential")
	}
}

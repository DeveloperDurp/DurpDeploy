package handler_test

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/url"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestSecurity_PasskeyDeleteRetainsBrowserSessionAfterOwnedNonLastDeletion(
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
	serverURL, err := url.Parse(h.server)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	client := newJar(t)
	client.Jar.SetCookies(serverURL, []*http.Cookie{{
		Name: "session", Value: current.sessionToken,
	}})
	current.client = &client
	credentialID := []byte("deletable")
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(current.user.ID, credentialID),
	); err != nil {
		t.Fatalf("create deletable passkey: %v", err)
	}
	remaining := securityCredentialParams(current.user.ID, []byte("remaining"))
	remaining.Name = "remaining"
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		remaining,
	); err != nil {
		t.Fatalf("create remaining passkey: %v", err)
	}
	apiToken, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint API token: %v", err)
	}
	if _, err := h.repo.Queries.CreateApiToken(
		context.Background(),
		db.CreateApiTokenParams{
			ID:          "passkey-delete-token",
			UserID:      current.user.ID,
			Name:        "preserved",
			TokenPrefix: prefix,
			TokenHash:   hash,
			Scope:       "global",
		},
	); err != nil {
		t.Fatalf("create API token: %v", err)
	}

	// When
	response, err := current.client.PostForm(
		h.server+"/settings/security/passkeys/delete",
		url.Values{
			"credential_id": {
				base64.RawURLEncoding.EncodeToString(credentialID),
			},
			"csrf_token": {current.csrfToken},
		},
	)
	if err != nil {
		t.Fatalf("POST no-JS passkey delete: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/settings/security" {
		t.Fatalf(
			"non-last delete response = %d %q",
			response.StatusCode,
			response.Header.Get("Location"),
		)
	}
	securityPage, err := current.client.Get(h.server + "/settings/security")
	if err != nil {
		t.Fatalf("GET Security after passkey delete: %v", err)
	}
	securityPage.Body.Close()
	if securityPage.StatusCode != http.StatusOK {
		t.Fatalf(
			"Security after passkey delete = %d, want 200",
			securityPage.StatusCode,
		)
	}
	tokenCount, err := h.repo.Queries.CountApiTokensByUser(
		context.Background(),
		current.user.ID,
	)
	if err != nil || apiToken == "" || tokenCount != 1 {
		t.Fatalf("API token state = %d, error %v", tokenCount, err)
	}
}

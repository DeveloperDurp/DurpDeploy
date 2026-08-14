package handler_test

import (
	"context"
	"database/sql"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestSecurityPage_DisablesLastPasskeyDeletion(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	session := seedSession(t, h.repo, h.server, "admin")
	if _, err := h.repo.Queries.CreateWebAuthnCredential(
		context.Background(),
		securityCredentialParams(session.user.ID, []byte("only-passkey")),
	); err != nil {
		t.Fatalf("create only passkey: %v", err)
	}

	// When
	response, err := session.client.Get(h.server + "/settings/security")
	if err != nil {
		t.Fatalf("GET security page: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read security page: %v", err)
	}

	// Then
	pattern := regexp.MustCompile(
		`(?s)<form method="post" action="/settings/security/passkeys/delete"[^>]*>.*?<button[^>]*disabled[^>]*>Delete</button>.*?Keep another factor configured before deleting this passkey.`,
	)
	if response.StatusCode != http.StatusOK || !pattern.Match(body) {
		t.Fatalf(
			"last passkey deletion control = %d %q",
			response.StatusCode,
			body,
		)
	}
}

func TestSecurityPage_EnablesNonLastPasskeyDeletion(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	session := seedSession(t, h.repo, h.server, "admin")
	first := securityCredentialParams(session.user.ID, []byte("first-passkey"))
	second := securityCredentialParams(
		session.user.ID,
		[]byte("second-passkey"),
	)
	second.Name = "secondary"
	for _, credential := range []db.CreateWebAuthnCredentialParams{first, second} {
		if _, err := h.repo.Queries.CreateWebAuthnCredential(
			context.Background(),
			credential,
		); err != nil {
			t.Fatalf("create passkey: %v", err)
		}
	}

	// When
	response, err := session.client.Get(h.server + "/settings/security")
	if err != nil {
		t.Fatalf("GET security page: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read security page: %v", err)
	}

	// Then
	content := string(body)
	dialogID := "passkey-delete-dialog-" +
		base64.RawURLEncoding.EncodeToString(first.CredentialID)
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(
			content,
			`data-passkey-delete-dialog="`+dialogID+`"`,
		) ||
		!strings.Contains(content, `<dialog id="`+dialogID+`"`) ||
		!strings.Contains(
			content,
			`aria-labelledby="`+dialogID+`-title"`,
		) ||
		!strings.Contains(
			content,
			`aria-describedby="`+dialogID+`-description"`,
		) ||
		!strings.Contains(content, `data-passkey-delete-confirm>`) ||
		!strings.Contains(content, `>Cancel</button>`) ||
		!strings.Contains(content, `>Confirm delete</button>`) ||
		strings.Contains(content, "Keep another factor configured") {
		t.Fatalf(
			"non-last passkey deletion control = %d %q",
			response.StatusCode,
			body,
		)
	}
}

func TestSecurityPage_DisableMFAConfirmation_whenMFAEnabled(t *testing.T) {
	// Given an authenticated user with MFA enabled and a fresh session.
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	session := seedSession(t, h.repo, h.server, "admin")
	seedTOTP(t, h, *session.user, box)
	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{
			ID: session.sessionToken,
			ReauthenticatedAt: sql.NullInt64{
				Int64: time.Now().Unix(),
				Valid: true,
			},
		},
	); err != nil {
		t.Fatalf("mark session reauthenticated: %v", err)
	}

	// When the user loads Security.
	response, err := session.client.Get(h.server + "/settings/security")
	if err != nil {
		t.Fatalf("GET security page: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatalf("read security page: %v", err)
	}

	// Then the disable form opens an accessible confirmation dialog.
	content := string(body)
	const dialogID = "security-disable-mfa-dialog"
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(
			content,
			`data-security-disable-dialog="`+dialogID+`"`,
		) ||
		!strings.Contains(content, `<dialog id="`+dialogID+`"`) ||
		!strings.Contains(
			content,
			`aria-labelledby="`+dialogID+`-title"`,
		) ||
		!strings.Contains(
			content,
			`aria-describedby="`+dialogID+`-description"`,
		) ||
		!strings.Contains(content, `data-security-disable-confirm>`) ||
		!strings.Contains(content, `>Cancel</button>`) ||
		!strings.Contains(content, `>Confirm disable</button>`) {
		t.Fatalf(
			"disable confirmation dialog = %d %q",
			response.StatusCode,
			body,
		)
	}

	// When JavaScript is unavailable, the original form posts directly.
	disableResponse, err := session.client.PostForm(
		h.server+"/settings/security/disable",
		url.Values{"csrf_token": {session.csrfToken}},
	)
	if err != nil {
		t.Fatalf("POST no-JS disable fallback: %v", err)
	}
	defer disableResponse.Body.Close()

	// Then the existing server-side redirect is retained.
	if disableResponse.StatusCode != http.StatusSeeOther ||
		disableResponse.Header.Get("Location") != "/login" {
		t.Fatalf(
			"no-JS disable = %d %q, want 303 login",
			disableResponse.StatusCode,
			disableResponse.Header.Get("Location"),
		)
	}
}

package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestSecurity_PasskeyVerificationCancelDiscardsStagedChallenge(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
		Enabled: true, Origin: "https://example.org", RPID: "example.org",
	}})
	current := seedSession(t, h.repo, h.server, "admin")
	pending, err := mfa.NewChallengeService(mfa.ChallengeServiceConfig{Repository: h.repo}).
		Issue(
			context.Background(),
			mfa.ChallengeIssue{
				UserID:    current.user.ID,
				SessionID: current.sessionToken,
				Purpose:   mfa.ChallengePurposeWebAuthnAuth,
			},
		)
	if err != nil {
		t.Fatalf("issue staged passkey challenge: %v", err)
	}
	serverURL, err := url.Parse(h.server)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	current.client.Jar.SetCookies(serverURL, []*http.Cookie{
		{
			Name:  "passkey_verification_token",
			Value: pending.Token,
			Path:  "/settings/security/passkeys/test",
		},
		{
			Name:  "passkey_verification_csrf",
			Value: pending.CSRF,
			Path:  "/settings/security/passkeys/test",
		},
	})

	// When
	testPage, err := current.client.Get(
		h.server + "/settings/security/passkeys/test",
	)
	if err != nil {
		t.Fatalf("get passkey test page: %v", err)
	}
	body, err := io.ReadAll(testPage.Body)
	testPage.Body.Close()
	if err != nil {
		t.Fatalf("read passkey test page: %v", err)
	}
	cancel := postSecurityValues(
		t,
		current,
		h.server,
		"/settings/security/passkeys/test/cancel",
		securityHiddenValues(t, string(body)),
	)
	defer cancel.Body.Close()

	// Then
	if testPage.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), "Test your passkey") ||
		strings.Contains(string(body), `class="recovery-code `) ||
		cancel.StatusCode != http.StatusSeeOther ||
		cancel.Header.Get("Location") != "/settings/security" {
		t.Fatalf(
			"passkey test/cancel responses = %d / %d %q",
			testPage.StatusCode,
			cancel.StatusCode,
			cancel.Header.Get("Location"),
		)
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?",
		current.user.ID,
	) != 0 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
			current.user.ID,
		) != 0 {
		t.Fatal("passkey cancel retained a staged credential or challenge")
	}
}

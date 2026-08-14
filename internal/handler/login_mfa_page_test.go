package handler_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestMFAChallengePage_RendersAccessibleConfiguredTOTPFactor(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "challenge-page@example.com", "hunter2")
	seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)

	// When
	response, err := pending.client.Get(h.server + "/login/mfa")
	if err != nil {
		t.Fatalf("get MFA challenge page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read MFA challenge page: %v", err)
	}
	markup := string(body)

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store",
			response.Header.Get("Cache-Control"))
	}
	for _, required := range []string{
		"Choose a verification method",
		`action="/login/mfa/totp"`,
		`for="totp-code"`,
		`id="totp-code"`,
		`inputmode="numeric"`,
		`autocomplete="one-time-code"`,
		`pattern="[0-9]{6}"`,
		`maxlength="6"`,
		`aria-describedby="totp-code-help"`,
		`id="totp-code-help"`,
		`action="/login/mfa/cancel"`,
		"This verification expires after five minutes.",
	} {
		if !strings.Contains(markup, required) {
			t.Errorf("MFA challenge page is missing %q", required)
		}
	}
	for _, unavailable := range []string{
		`action="/login/mfa/recovery"`,
		`id="login-mfa-passkey"`,
	} {
		if strings.Contains(markup, unavailable) {
			t.Errorf("MFA challenge page rendered unavailable %q", unavailable)
		}
	}
	if strings.Count(markup, pending.csrf) != 2 {
		t.Fatalf("pending CSRF appears %d times, want one per form",
			strings.Count(markup, pending.csrf))
	}
	for _, forbidden := range []string{pending.token, user.Email, "mfa_pending"} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("MFA challenge page disclosed %q", forbidden)
		}
	}
}

func TestMFAChallengePage_RendersGenericAccessibleErrorWithoutSecrets(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "challenge-error@example.com", "hunter2")
	code := seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)

	// When
	response, err := pending.client.PostForm(
		h.server+"/login/mfa/totp",
		url.Values{
			"code":       {"000000"},
			"csrf_token": {pending.csrf},
		},
	)
	if err != nil {
		t.Fatalf("post invalid MFA factor: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read invalid MFA factor response: %v", err)
	}
	markup := string(body)

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", response.StatusCode,
			http.StatusUnprocessableEntity)
	}
	for _, required := range []string{
		`role="alert"`,
		`aria-live="assertive"`,
		"Unable to verify authentication factor",
		`action="/login/mfa/totp"`,
	} {
		if !strings.Contains(markup, required) {
			t.Errorf("generic MFA error page is missing %q", required)
		}
	}
	for _, unavailable := range []string{
		`action="/login/mfa/recovery"`,
		`id="login-mfa-passkey"`,
	} {
		if strings.Contains(markup, unavailable) {
			t.Errorf(
				"generic MFA error page rendered unavailable %q",
				unavailable,
			)
		}
	}
	for _, forbidden := range []string{pending.token, user.Email, code, "mfa_pending"} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("generic MFA error page disclosed %q", forbidden)
		}
	}
}

func TestMFAChallengePage_HidesFactorsBeforePasswordVerification(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "password-page@example.com", "hunter2")
	seedTOTP(t, h, user, box)

	// When
	response, err := http.Get(h.server + "/login")
	if err != nil {
		t.Fatalf("get password page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read password page: %v", err)
	}
	markup := string(body)

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	for _, forbidden := range []string{
		"Authenticator code",
		"Recovery code",
		"Passkey",
		user.Email,
		"mfa_pending",
	} {
		if strings.Contains(markup, forbidden) {
			t.Errorf("password page disclosed MFA detail %q", forbidden)
		}
	}
}

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

func TestLogin_MFAHiddenTOTPRejectsMalformedSubmission(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "hidden-totp@example.com", "hunter2")
	seedLoginMFAPasskey(t, h, user.ID)
	pending := loginPending(t, h, user.Email)

	// When
	response, err := pending.client.PostForm(
		h.server+"/login/mfa/totp",
		url.Values{"code": {"not-a-code"}, "csrf_token": {pending.csrf}},
	)
	if err != nil {
		t.Fatalf("post hidden TOTP: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status = %d, want %d",
			response.StatusCode,
			http.StatusUnprocessableEntity,
		)
	}
	if pendingMFAChallengeAttempts(t, h, user.ID) != 0 {
		t.Fatal("hidden TOTP submission recorded an MFA failure")
	}
}

func TestLogin_MFAHiddenRecoveryPreservesAvailableTOTP(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "hidden-recovery@example.com", "hunter2")
	code := seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)

	// When
	rejected, err := pending.client.PostForm(
		h.server+"/login/mfa/recovery",
		url.Values{
			"code":       {"not-a-recovery-code"},
			"csrf_token": {pending.csrf},
		},
	)
	if err != nil {
		t.Fatalf("post hidden recovery code: %v", err)
	}
	defer rejected.Body.Close()

	// Then
	if rejected.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status = %d, want %d",
			rejected.StatusCode,
			http.StatusUnprocessableEntity,
		)
	}
	if pendingMFAChallengeAttempts(t, h, user.ID) != 0 {
		t.Fatal("hidden recovery submission recorded an MFA failure")
	}

	// When
	completed, err := pending.client.PostForm(
		h.server+"/login/mfa/totp",
		url.Values{"code": {code}, "csrf_token": {pending.csrf}},
	)
	if err != nil {
		t.Fatalf("post available TOTP: %v", err)
	}
	defer completed.Body.Close()

	// Then
	if completed.StatusCode != http.StatusSeeOther ||
		completed.Header.Get("Location") != "/" {
		t.Fatalf(
			"TOTP response = %d %q",
			completed.StatusCode,
			completed.Header.Get("Location"),
		)
	}
}

func TestLogin_MFAHiddenPasskeyBeginPreservesAvailableTOTP(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{WebAuthn: mfa.WebAuthnConfig{
		Enabled: true, Origin: "https://example.org", RPID: "example.org",
	}})
	user := h.seedUser(t, "hidden-passkey@example.com", "hunter2")
	code := seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)
	request, err := http.NewRequest(
		http.MethodPost,
		h.server+"/login/mfa/webauthn/begin",
		nil,
	)
	if err != nil {
		t.Fatalf("new hidden passkey request: %v", err)
	}
	request.Header.Set("X-CSRF-Token", pending.csrf)

	// When
	rejected, err := pending.client.Do(request)
	if err != nil {
		t.Fatalf("post hidden passkey: %v", err)
	}
	defer rejected.Body.Close()
	body, err := io.ReadAll(rejected.Body)
	if err != nil {
		t.Fatalf("read hidden passkey response: %v", err)
	}

	// Then
	if rejected.StatusCode != http.StatusUnprocessableEntity ||
		!strings.Contains(
			rejected.Header.Get("Content-Type"),
			"application/json",
		) ||
		!strings.Contains(
			string(body),
			"Unable to verify authentication factor",
		) {
		t.Fatalf("hidden passkey response = %d %q", rejected.StatusCode, body)
	}
	if pendingMFAChallengeAttempts(t, h, user.ID) != 0 {
		t.Fatal("hidden passkey submission recorded an MFA failure")
	}

	// When
	completed, err := pending.client.PostForm(
		h.server+"/login/mfa/totp",
		url.Values{"code": {code}, "csrf_token": {pending.csrf}},
	)
	if err != nil {
		t.Fatalf("post available TOTP: %v", err)
	}
	defer completed.Body.Close()

	// Then
	if completed.StatusCode != http.StatusSeeOther ||
		completed.Header.Get("Location") != "/" {
		t.Fatalf(
			"TOTP response = %d %q",
			completed.StatusCode,
			completed.Header.Get("Location"),
		)
	}
}

func TestLogin_MFAAvailabilityFailsClosedWhenRecoveryQueryFails(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "mfa-availability-error@example.com", "hunter2")
	seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)
	if _, err := h.repo.DB.ExecContext(
		context.Background(),
		"DROP TABLE mfa_recovery_codes",
	); err != nil {
		t.Fatalf("drop recovery code table: %v", err)
	}

	// When
	response, err := pending.client.Get(h.server + "/login/mfa")
	if err != nil {
		t.Fatalf("get MFA page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read MFA page: %v", err)
	}
	markup := string(body)

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	for _, method := range unavailableLoginMFAMethods() {
		if strings.Contains(markup, method) {
			t.Errorf(
				"MFA page rendered %q after availability query error",
				method,
			)
		}
	}
}

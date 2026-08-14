package handler_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/mfa"
)

type stagedTOTP struct {
	challenge url.Values
	seed      string
}

func TestSecurity_TOTPEnrollmentActivatesWithOneSubmission(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	staged := stageTOTP(t, h, current)

	// When
	response := postSecurityValues(
		t,
		current,
		h.server,
		"/settings/security/totp/confirm",
		enrollmentValues(
			staged,
			totpCode(t, staged.seed, time.Now()),
		),
	)
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read enrollment response: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusOK ||
		strings.Count(string(body), `class="recovery-code `) != 10 {
		t.Fatalf(
			"enrolled TOTP response = %d with %d codes",
			response.StatusCode,
			strings.Count(string(body), `class="recovery-code `),
		)
	}
	if strings.Contains(string(body), staged.seed) ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
			current.user.ID,
		) != 1 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
			current.user.ID,
		) != 10 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
			current.user.ID,
		) != 0 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
			current.user.ID,
		) != 0 {
		t.Fatal(
			"successful enrollment did not atomically activate the submitted factor",
		)
	}
}

func TestSecurity_RecoveryContinueInvalidatesBrowserStateAndRedirectsLogin(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSession(t, h.repo, h.server, "admin")
	staged := stageTOTP(t, h, current)
	verified := postSecurityValues(
		t,
		current,
		h.server,
		"/settings/security/totp/confirm",
		enrollmentValues(
			staged,
			totpCode(t, staged.seed, time.Now()),
		),
	)
	verified.Body.Close()

	// When
	response := postSecurityValues(
		t,
		current,
		h.server,
		"/settings/security/recovery/continue",
		url.Values{"csrf_token": {current.csrfToken}},
	)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/login" {
		t.Fatalf(
			"recovery continue = %d %q",
			response.StatusCode,
			response.Header.Get("Location"),
		)
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		current.user.ID,
	) != 0 ||
		countSecurityRows(
			t,
			h,
			"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
			current.user.ID,
		) != 0 {
		t.Fatal("recovery continue retained browser session or challenge state")
	}
}

func TestSecurity_RecoveryContinueRejectsMissingCSRFTokens(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	current := seedSession(t, h.repo, h.server, "admin")

	// When
	req, err := http.NewRequest(
		http.MethodPost,
		h.server+"/settings/security/recovery/continue",
		http.NoBody,
	)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: current.sessionToken})
	response, err := (&http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}).Do(req)
	if err != nil {
		t.Fatalf("POST /settings/security/recovery/continue: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf(
			"recovery continue status = %d, want %d",
			response.StatusCode,
			http.StatusForbidden,
		)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}
	if !strings.Contains(string(body), "Invalid CSRF token") {
		t.Fatalf(
			"response = %q, want invalid CSRF token",
			strings.TrimSpace(string(body)),
		)
	}
	if countSecurityRows(
		t,
		h,
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		current.user.ID,
	) != 1 {
		t.Fatal("missing CSRF token cleared browser session")
	}
}

func stageTOTP(
	t *testing.T,
	h *authHarness,
	current *authedSession,
) stagedTOTP {
	t.Helper()
	begin := postSecurityValues(
		t,
		current,
		h.server,
		"/settings/security/totp/begin",
		url.Values{"csrf_token": {current.csrfToken}},
	)
	defer begin.Body.Close()
	body, err := io.ReadAll(begin.Body)
	if err != nil {
		t.Fatalf("read TOTP setup page: %v", err)
	}
	manualKey := manualTOTPKey.FindStringSubmatch(string(body))
	if begin.StatusCode != http.StatusOK || len(manualKey) != 2 {
		t.Fatalf(
			"TOTP setup response = %d with key %q",
			begin.StatusCode,
			manualKey,
		)
	}
	setup := securityHiddenValues(t, string(body))
	return stagedTOTP{
		challenge: setup,
		seed:      manualKey[1],
	}
}

func enrollmentValues(staged stagedTOTP, code string) url.Values {
	return url.Values{
		"challenge_csrf":  {staged.challenge.Get("challenge_csrf")},
		"challenge_token": {staged.challenge.Get("challenge_token")},
		"code":            {code},
		"csrf_token":      {staged.challenge.Get("csrf_token")},
	}
}

func postSecurityValues(
	t *testing.T,
	current *authedSession,
	serverURL, path string,
	values url.Values,
) *http.Response {
	t.Helper()
	response, err := current.client.PostForm(serverURL+path, values)
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	return response
}

func totpCode(t *testing.T, seed string, at time.Time) string {
	t.Helper()
	code, err := mfa.GenerateTOTPCode(seed, at)
	if err != nil {
		t.Fatalf("generate TOTP code: %v", err)
	}
	return code
}

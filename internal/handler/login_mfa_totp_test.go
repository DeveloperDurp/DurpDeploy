package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/mfa"
)

func TestLogin_MFAChallengeRequiresPendingCookieAndDisablesCaching(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	client := newJar(t)

	// When
	response, err := client.Get(h.server + "/login/mfa")
	if err != nil {
		t.Fatalf("get MFA login page: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/login" {
		t.Fatalf("missing-pending response = %d %q", response.StatusCode,
			response.Header.Get("Location"))
	}

	// Given
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "totp-page@example.com", "hunter2")
	seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)

	// When
	response, err = pending.client.Get(h.server + "/login/mfa")
	if err != nil {
		t.Fatalf("get pending MFA login page: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("MFA page status = %d, want 200", response.StatusCode)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"Cache-Control = %q, want no-store",
			response.Header.Get("Cache-Control"),
		)
	}
}

func TestLogin_TOTPCompletesPendingChallengeAndIssuesFinalSession(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "totp@example.com", "hunter2")
	code := seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)

	// When
	response, err := pending.client.PostForm(
		h.server+"/login/mfa/totp",
		url.Values{
			"code":       {code},
			"csrf_token": {pending.csrf},
		},
	)
	if err != nil {
		t.Fatalf("post TOTP: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/" {
		t.Fatalf("TOTP response = %d %q", response.StatusCode,
			response.Header.Get("Location"))
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"Cache-Control = %q, want no-store",
			response.Header.Get("Cache-Control"),
		)
	}
	pendingCookiesCleared(t, response)
	if sessionCount(t, h, user.ID) != 1 {
		t.Fatal("TOTP completion did not create exactly one final session")
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "session" && cookie.Value == pending.token {
			t.Fatal("final browser session reused the pending MFA token")
		}
	}
	logs, err := h.repo.Queries.ListAuditLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(logs) != 1 || logs[0].Action != "mfa_login_factor" {
		t.Fatalf("audit rows = %#v, want one final MFA login audit", logs)
	}
}

func TestLogin_TOTPRejectsInvalidCSRFExpiredAndThrottledChallenges(
	t *testing.T,
) {
	tests := []struct {
		name string
		run  func(*testing.T, *authHarness, string, pendingLogin, int64)
	}{
		{
			name: "invalid factor is generic",
			run: func(t *testing.T, h *authHarness, _ string, pending pendingLogin, userID int64) {
				response, err := pending.client.PostForm(
					h.server+"/login/mfa/totp",
					url.Values{
						"code":       {"000000"},
						"csrf_token": {pending.csrf},
					},
				)
				if err != nil {
					t.Fatalf("post invalid TOTP: %v", err)
				}
				defer response.Body.Close()
				body, _ := io.ReadAll(response.Body)
				if response.StatusCode != http.StatusUnprocessableEntity ||
					!strings.Contains(
						string(body),
						"Unable to verify authentication factor",
					) ||
					strings.Contains(string(body), "totp-invalid@example.com") {
					t.Fatalf(
						"invalid factor response = %d %q",
						response.StatusCode,
						body,
					)
				}
				if sessionCount(t, h, userID) != 0 {
					t.Fatal("invalid TOTP created a final session")
				}
			},
		},
		{
			name: "invalid csrf is forbidden",
			run: func(t *testing.T, h *authHarness, code string, pending pendingLogin, userID int64) {
				response, err := pending.client.PostForm(
					h.server+"/login/mfa/totp",
					url.Values{
						"code":       {code},
						"csrf_token": {"wrong"},
					},
				)
				if err != nil {
					t.Fatalf("post invalid CSRF: %v", err)
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusForbidden ||
					sessionCount(t, h, userID) != 0 {
					t.Fatalf("invalid CSRF response = %d", response.StatusCode)
				}
			},
		},
		{
			name: "expired challenge clears cookies",
			run: func(t *testing.T, h *authHarness, code string, pending pendingLogin, userID int64) {
				if _, err := h.repo.DB.ExecContext(
					context.Background(),
					"UPDATE mfa_challenges SET expires_at = ?",
					time.Now().Add(-time.Second).Unix(),
				); err != nil {
					t.Fatalf("expire challenge: %v", err)
				}
				response, err := pending.client.PostForm(
					h.server+"/login/mfa/totp",
					url.Values{
						"code":       {code},
						"csrf_token": {pending.csrf},
					},
				)
				if err != nil {
					t.Fatalf("post expired TOTP: %v", err)
				}
				defer response.Body.Close()
				if response.StatusCode != http.StatusUnprocessableEntity ||
					sessionCount(t, h, userID) != 0 {
					t.Fatalf("expired TOTP response = %d", response.StatusCode)
				}
				pendingCookiesCleared(t, response)
			},
		},
		{
			name: "throttle blocks sixth failure",
			run: func(t *testing.T, h *authHarness, _ string, pending pendingLogin, userID int64) {
				for attempt := 0; attempt < 6; attempt++ {
					response, err := pending.client.PostForm(
						h.server+"/login/mfa/totp",
						url.Values{
							"code":       {"000000"},
							"csrf_token": {pending.csrf},
						},
					)
					if err != nil {
						t.Fatalf("post invalid TOTP %d: %v", attempt, err)
					}
					response.Body.Close()
					want := http.StatusUnprocessableEntity
					if attempt == 5 {
						want = http.StatusTooManyRequests
					}
					if response.StatusCode != want {
						t.Fatalf(
							"attempt %d status = %d, want %d",
							attempt,
							response.StatusCode,
							want,
						)
					}
				}
				if sessionCount(t, h, userID) != 0 {
					t.Fatal("throttled TOTP created a final session")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			box := configureMFA(t, h, mfa.Config{})
			user := h.seedUser(t, "totp-invalid@example.com", "hunter2")
			code := seedTOTP(t, h, user, box)
			pending := loginPending(t, h, user.Email)

			// When
			test.run(t, h, code, pending, user.ID)
		})
	}
}

func TestLogin_MFACancelClearsPendingState(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "cancel@example.com", "hunter2")
	seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)

	// When
	response, err := pending.client.PostForm(
		h.server+"/login/mfa/cancel",
		url.Values{
			"csrf_token": {pending.csrf},
		},
	)
	if err != nil {
		t.Fatalf("cancel MFA login: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/login" {
		t.Fatalf("cancel response = %d %q", response.StatusCode,
			response.Header.Get("Location"))
	}
	pendingCookiesCleared(t, response)
	if sessionCount(t, h, user.ID) != 0 {
		t.Fatal("cancelled MFA login created a final session")
	}
}

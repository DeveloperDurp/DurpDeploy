package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/secret"
)

type pendingLogin struct {
	client *http.Client
	csrf   string
	token  string
}

func configureMFA(t *testing.T, h *authHarness, config mfa.Config) *secret.Box {
	t.Helper()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	h.authHandler.SetMFAService(mfa.NewService(config, box))
	return box
}

func seedTOTP(
	t *testing.T,
	h *authHarness,
	user db.User,
	box *secret.Box,
) string {
	t.Helper()
	const seed = "JBSWY3DPEHPK3PXP"
	encrypted, err := box.Encrypt(seed)
	if err != nil {
		t.Fatalf("encrypt TOTP seed: %v", err)
	}
	if _, err := h.repo.Queries.CreateTOTP(
		context.Background(),
		db.CreateTOTPParams{
			UserID:        user.ID,
			EncryptedSeed: []byte(encrypted),
			LastAcceptedStep: sql.NullInt64{
				Valid: false,
			},
		},
	); err != nil {
		t.Fatalf("create TOTP factor: %v", err)
	}
	code, err := mfa.GenerateTOTPCode(seed, time.Now())
	if err != nil {
		t.Fatalf("generate TOTP code: %v", err)
	}
	return code
}

func loginPending(
	t *testing.T,
	h *authHarness,
	email string,
) pendingLogin {
	t.Helper()
	client := newJar(t)
	response, err := client.PostForm(h.server+"/login", url.Values{
		"email":    {email},
		"password": {"hunter2"},
	})
	if err != nil {
		t.Fatalf("post login: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/login/mfa" {
		t.Fatalf("pending login response = %d %q", response.StatusCode,
			response.Header.Get("Location"))
	}

	pending := pendingLogin{client: &client}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "session" {
			t.Fatal("pending login set final session cookie")
		}
		if cookie.Name == "mfa_csrf" {
			pending.csrf = cookie.Value
		}
		if cookie.Name == "mfa_pending" {
			pending.token = cookie.Value
		}
	}
	if pending.csrf == "" || pending.token == "" {
		t.Fatal("pending login omitted MFA credentials")
	}
	return pending
}

func issueLoginMFAChallenge(
	t *testing.T,
	h *authHarness,
	userID int64,
) pendingLogin {
	t.Helper()
	challenge, err := mfa.NewChallengeService(
		mfa.ChallengeServiceConfig{Repository: h.repo},
	).Issue(context.Background(), mfa.ChallengeIssue{
		UserID: userID, Purpose: mfa.ChallengePurposeLoginMFA,
	})
	if err != nil {
		t.Fatalf("issue login MFA challenge: %v", err)
	}
	serverURL, err := url.Parse(h.server)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	client := newJar(t)
	client.Jar.SetCookies(serverURL, []*http.Cookie{
		{Name: "mfa_pending", Value: challenge.Token, Path: "/"},
		{Name: "mfa_csrf", Value: challenge.CSRF, Path: "/"},
	})
	return pendingLogin{
		client: &client,
		csrf:   challenge.CSRF,
		token:  challenge.Token,
	}
}

func sessionCount(t *testing.T, h *authHarness, userID int64) int {
	t.Helper()
	var count int
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		userID,
	).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	return count
}

func pendingMFAChallengeAttempts(
	t *testing.T,
	h *authHarness,
	userID int64,
) int64 {
	t.Helper()
	var attempts int64
	if err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT attempts FROM mfa_challenges WHERE user_id = ?",
		userID,
	).Scan(&attempts); err != nil {
		t.Fatalf("load pending MFA challenge: %v", err)
	}
	return attempts
}

func pendingCookiesCleared(t *testing.T, response *http.Response) {
	t.Helper()
	cleared := map[string]bool{}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "mfa_pending" || cookie.Name == "mfa_csrf" {
			cleared[cookie.Name] = cookie.MaxAge < 0
		}
	}
	if !cleared["mfa_pending"] || !cleared["mfa_csrf"] {
		t.Fatalf("pending cookies not cleared: %#v", cleared)
	}
}

package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestLogin_RecoveryCompletesPendingChallengeOnlyOnce(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "recovery@example.com", "hunter2")
	seedTOTP(t, h, user, box)
	const recoveryCode = "0123456789ABCDEF0123456789ABCDEF"
	hash, err := mfa.HashRecoveryCode(recoveryCode)
	if err != nil {
		t.Fatalf("hash recovery code: %v", err)
	}
	if _, err := h.repo.Queries.CreateRecoveryCode(
		context.Background(),
		db.CreateRecoveryCodeParams{
			ID: "recovery-test", UserID: user.ID, CodeHash: hash[:],
		},
	); err != nil {
		t.Fatalf("create recovery code: %v", err)
	}
	pending := loginPending(t, h, user.Email)

	// When
	response, err := pending.client.PostForm(
		h.server+"/login/mfa/recovery",
		url.Values{
			"code":       {recoveryCode},
			"csrf_token": {pending.csrf},
		},
	)
	if err != nil {
		t.Fatalf("post recovery code: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/" {
		t.Fatalf("recovery response = %d %q", response.StatusCode,
			response.Header.Get("Location"))
	}
	pendingCookiesCleared(t, response)
	if sessionCount(t, h, user.ID) != 1 {
		t.Fatal("recovery completion did not create one final session")
	}
	logs, err := h.repo.Queries.ListAuditLogs(context.Background(), 10)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	if len(logs) != 2 || logs[0].Action != "mfa_login_factor" ||
		logs[1].Action != "mfa_recovery_use" {
		t.Fatalf(
			"audit rows = %#v, want recovery use and final MFA login",
			logs,
		)
	}
	for _, log := range logs {
		if strings.Contains(log.Details.String, recoveryCode) {
			t.Fatal("recovery audit details contain the recovery code")
		}
	}

	// Given
	serverURL, err := url.Parse(h.server)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("new replay cookie jar: %v", err)
	}
	jar.SetCookies(serverURL, []*http.Cookie{
		{Name: "mfa_pending", Value: pending.token, Path: "/"},
		{Name: "mfa_csrf", Value: pending.csrf, Path: "/"},
	})
	replayClient := &http.Client{
		Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// When
	replay, err := replayClient.PostForm(
		h.server+"/login/mfa/recovery",
		url.Values{
			"code":       {recoveryCode},
			"csrf_token": {pending.csrf},
		},
	)
	if err != nil {
		t.Fatalf("post replayed recovery code: %v", err)
	}
	defer replay.Body.Close()

	// Then
	body, _ := io.ReadAll(replay.Body)
	if replay.StatusCode != http.StatusUnprocessableEntity ||
		sessionCount(t, h, user.ID) != 1 {
		t.Fatalf("replayed recovery response = %d %q", replay.StatusCode, body)
	}
}

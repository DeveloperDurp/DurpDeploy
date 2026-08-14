package handler_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/mfa"
)

func TestLogin_MFAGetRejectsExpiredPendingChallenge(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	box := configureMFA(t, h, mfa.Config{})
	user := h.seedUser(t, "expired-mfa-get@example.com", "hunter2")
	seedTOTP(t, h, user, box)
	pending := loginPending(t, h, user.Email)
	if _, err := h.repo.DB.ExecContext(
		context.Background(),
		"UPDATE mfa_challenges SET expires_at = ?",
		time.Now().Add(-time.Second).Unix(),
	); err != nil {
		t.Fatalf("expire challenge: %v", err)
	}

	// When
	response, err := pending.client.Get(h.server + "/login/mfa")
	if err != nil {
		t.Fatalf("get expired MFA login page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read expired MFA login page: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/login" {
		t.Fatalf("expired MFA page response = %d %q", response.StatusCode,
			response.Header.Get("Location"))
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"Cache-Control = %q, want no-store",
			response.Header.Get("Cache-Control"),
		)
	}
	if strings.Contains(string(body), "Authenticator code") {
		t.Fatal("expired MFA challenge rendered a factor page")
	}
	pendingCookiesCleared(t, response)
}

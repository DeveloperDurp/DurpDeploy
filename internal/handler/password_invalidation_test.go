package handler_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestWebPasswordUpdate_invalidates_browser_sessions_and_mfa_challenges(
	t *testing.T,
) {
	// Given
	h := newProjectHarness(t)
	target := seedSessionAs(
		t,
		h.repo,
		h.server.URL,
		"password-target@example.com",
		"deployer",
	)
	challenges := mfa.NewChallengeService(mfa.ChallengeServiceConfig{
		Repository: h.repo,
	})
	pending, err := challenges.Issue(context.Background(), mfa.ChallengeIssue{
		UserID:  target.user.ID,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	})
	if err != nil {
		t.Fatalf("issue MFA challenge: %v", err)
	}
	form := url.Values{
		"name":                  {"Password Target"},
		"role":                  {"deployer"},
		"password":              {"new-password-1"},
		"password_confirmation": {"new-password-1"},
	}
	form.Set("csrf_token", h.csrfToken())
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPut,
		fmt.Sprintf("%s/admin/users/%d", h.server.URL, target.user.ID),
		bytes.NewBufferString(form.Encode()),
	)
	if err != nil {
		t.Fatalf("create password update request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", h.csrfToken())

	// When
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("update password: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusSeeOther &&
		resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 303 or 200", resp.StatusCode)
	}
	if _, err := h.repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID: target.sessionToken,
		},
	); err != sql.ErrNoRows {
		t.Fatalf("password update left browser session active: %v", err)
	}
	if err := challenges.Consume(context.Background(), mfa.ChallengeBinding{
		Token:   pending.Token,
		CSRF:    pending.CSRF,
		UserID:  target.user.ID,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	}, func(context.Context, db.MfaChallenge) error {
		return nil
	}); !errors.Is(err, mfa.ErrChallengeInvalid) {
		t.Fatalf("password update left MFA challenge active: %v", err)
	}
}

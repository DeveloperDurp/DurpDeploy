package api_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestAPIPasswordUpdate_invalidates_browser_state_but_preserves_api_token(
	t *testing.T,
) {
	// Given
	h := newHarness(t)
	adminToken := h.adminToken(t)
	target := h.seedUser(t, "password-target@example.com", "deployer")
	apiToken := h.seedToken(t, target)
	sessionID, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("create browser session credentials: %v", err)
	}
	if _, err := h.repo.Queries.CreateSession(
		context.Background(),
		db.CreateSessionParams{
			ID:        sessionID,
			UserID:    target.ID,
			CsrfToken: csrf,
			ExpiresAt: time.Now().Add(24 * time.Hour).Unix(),
		},
	); err != nil {
		t.Fatalf("create browser session: %v", err)
	}
	challenges := mfa.NewChallengeService(mfa.ChallengeServiceConfig{
		Repository: h.repo,
	})
	pending, err := challenges.Issue(context.Background(), mfa.ChallengeIssue{
		UserID:  target.ID,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	})
	if err != nil {
		t.Fatalf("issue MFA challenge: %v", err)
	}

	// When
	rec := h.request(
		t,
		http.MethodPut,
		"/api/v1/admin/users/"+itoa(target.ID),
		adminToken,
		`{"name":"Password Target","role":"deployer","password":"new-password-1"}`,
	)

	// Then
	h.assertStatus(t, rec, http.StatusOK)
	if _, err := h.repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID: sessionID,
		},
	); err != sql.ErrNoRows {
		t.Fatalf("password update left browser session active: %v", err)
	}
	if err := challenges.Consume(context.Background(), mfa.ChallengeBinding{
		Token:   pending.Token,
		CSRF:    pending.CSRF,
		UserID:  target.ID,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	}, func(context.Context, db.MfaChallenge) error {
		return nil
	}); !errors.Is(err, mfa.ErrChallengeInvalid) {
		t.Fatalf("password update left MFA challenge active: %v", err)
	}
	h.assertStatus(
		t,
		h.request(t, http.MethodGet, "/api/v1/projects", apiToken, ""),
		http.StatusOK,
	)
}

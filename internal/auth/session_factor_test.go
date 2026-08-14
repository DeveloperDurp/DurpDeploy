package auth_test

import (
	"context"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestSessionIssue_creates_no_session_when_factor_fails(t *testing.T) {
	// Given
	repo := newSessionRepository(t)
	user := seedSessionUser(t, repo)
	challenges := mfa.NewChallengeService(mfa.ChallengeServiceConfig{
		Repository: repo,
	})
	pending, err := challenges.Issue(context.Background(), mfa.ChallengeIssue{
		UserID:  user.ID,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	})
	if err != nil {
		t.Fatalf("issue MFA challenge: %v", err)
	}

	// When
	if err := challenges.RecordFailure(
		context.Background(),
		mfa.ChallengeBinding{
			Token:   pending.Token,
			CSRF:    pending.CSRF,
			UserID:  user.ID,
			Purpose: mfa.ChallengePurposeTOTPVerify,
		},
	); err != nil {
		t.Fatalf("record factor failure: %v", err)
	}

	// Then
	var sessions int
	if err := repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		user.ID,
	).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if sessions != 0 {
		t.Fatalf("sessions after failed factor = %d, want 0", sessions)
	}
}

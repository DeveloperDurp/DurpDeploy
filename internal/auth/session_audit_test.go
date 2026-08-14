package auth_test

import (
	"context"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestSessionIssue_invokesAuditOnceOnlyAfterSuccessfulFinalIssue(
	t *testing.T,
) {
	// Given
	repo := newSessionRepository(t)
	user := seedSessionUser(t, repo)
	audits := 0

	// When
	session, err := auth.IssueBrowserSession(
		context.Background(),
		repo,
		auth.BrowserSessionIssue{
			UserID: user.ID,
			Audit: func(db.Session) {
				audits++
			},
		},
	)
	if err != nil {
		t.Fatalf("issue final session: %v", err)
	}

	// Then
	if session.ID == "" || audits != 1 {
		t.Fatalf(
			"session=%q audits=%d, want final session and one audit",
			session.ID,
			audits,
		)
	}
}

package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestSecurityInvalidation_deletes_browser_state_but_preserves_api_token(
	t *testing.T,
) {
	// Given
	repo := newSessionRepository(t)
	user := seedSessionUser(t, repo)
	session, err := auth.IssueBrowserSession(
		context.Background(), repo, auth.BrowserSessionIssue{
			UserID: user.ID,
			Audit:  func(db.Session) {},
		},
	)
	if err != nil {
		t.Fatalf("issue browser session: %v", err)
	}
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
	token, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint API token: %v", err)
	}
	if _, err := repo.Queries.CreateApiToken(
		context.Background(),
		db.CreateApiTokenParams{
			ID:          "preserved-api-token",
			UserID:      user.ID,
			Name:        "preserved",
			TokenPrefix: prefix,
			TokenHash:   hash,
			Scope:       "global",
			ExpiresAt:   sql.NullInt64{},
		},
	); err != nil {
		t.Fatalf("create API token: %v", err)
	}

	// When
	if err := auth.InvalidateBrowserAuth(
		context.Background(),
		repo,
		user.ID,
	); err != nil {
		t.Fatalf("invalidate browser authentication: %v", err)
	}

	// Then
	if _, err := repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID: session.ID,
		},
	); err != sql.ErrNoRows {
		t.Fatalf("browser session remains after invalidation: %v", err)
	}
	if err := challenges.Consume(context.Background(), mfa.ChallengeBinding{
		Token:   pending.Token,
		CSRF:    pending.CSRF,
		UserID:  user.ID,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	}, func(context.Context, db.MfaChallenge) error {
		return nil
	}); !errors.Is(err, mfa.ErrChallengeInvalid) {
		t.Fatalf("MFA challenge remains after invalidation: %v", err)
	}
	handler := auth.ApiTokenMiddleware(repo)(http.HandlerFunc(
		func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
	))
	req := httptest.NewRequest(http.MethodGet, "/api/v1/projects", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf(
			"API token status = %d, want %d",
			rec.Code,
			http.StatusNoContent,
		)
	}
}

func TestUpdatePasswordRejectsStaleHashAndRollsBack(t *testing.T) {
	for _, failure := range []string{"stale hash", "session cleanup"} {
		t.Run(failure, func(t *testing.T) {
			repo := newSessionRepository(t)
			user := seedSessionUser(t, repo)
			session, err := auth.IssueBrowserSession(
				t.Context(),
				repo,
				auth.BrowserSessionIssue{
					UserID: user.ID,
					Audit:  func(db.Session) {},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			expected := user.PasswordHash
			if failure == "stale hash" {
				expected = "outdated"
			} else {
				if _, err := repo.DB.ExecContext(
					t.Context(),
					`CREATE TRIGGER fail_session_delete BEFORE DELETE ON sessions BEGIN SELECT RAISE(ABORT, 'cleanup failed'); END`,
				); err != nil {
					t.Fatal(err)
				}
			}
			err = auth.UpdatePassword(t.Context(), repo, auth.PasswordChange{
				UserID:       user.ID,
				Password:     "New-password-141",
				ExpectedHash: expected,
			})
			if err == nil {
				t.Fatal("password change unexpectedly succeeded")
			}
			if failure == "stale hash" &&
				!errors.Is(err, auth.ErrPasswordChanged) {
				t.Fatal(err)
			}
			stored, err := repo.Queries.GetUserByID(t.Context(), user.ID)
			if err != nil || stored.PasswordHash != user.PasswordHash {
				t.Fatal("failed transaction changed password")
			}
			if _, err := repo.Queries.GetSession(
				t.Context(),
				db.GetSessionParams{ID: session.ID},
			); err != nil {
				t.Fatalf("failed transaction removed session: %v", err)
			}
		})
	}
}

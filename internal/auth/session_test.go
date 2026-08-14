package auth_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func newSessionRepository(t *testing.T) *repository.Repository {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sessions.db")
	conn, err := migrate.Run(
		fmt.Sprintf("file:%s?_pragma=foreign_keys(1)", path),
	)
	if err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return repository.New(conn)
}

func seedSessionUser(t *testing.T, repo *repository.Repository) db.User {
	t.Helper()
	hash, err := auth.HashPassword("test-password")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := repo.Queries.CreateUser(
		context.Background(),
		db.CreateUserParams{
			Email:        "session@example.com",
			PasswordHash: hash,
			Name:         "Session User",
			Role:         "admin",
		},
	)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return user
}

func TestSessionIssue_creates_final_session_only_after_challenge_consumption(
	t *testing.T,
) {
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
		t.Fatalf("issue challenge: %v", err)
	}
	if _, err := repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID: pending.Token,
		},
	); err != sql.ErrNoRows {
		t.Fatalf("pending challenge created a browser session: %v", err)
	}
	var sessionsBeforeFactor int
	if err := repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
		user.ID,
	).Scan(&sessionsBeforeFactor); err != nil {
		t.Fatalf("count sessions before factor: %v", err)
	}
	if sessionsBeforeFactor != 0 {
		t.Fatalf("sessions before factor = %d, want 0", sessionsBeforeFactor)
	}

	// When
	var final db.Session
	err = challenges.Consume(context.Background(), mfa.ChallengeBinding{
		Token:   pending.Token,
		CSRF:    pending.CSRF,
		UserID:  user.ID,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	}, func(ctx context.Context, _ db.MfaChallenge) error {
		var issueErr error
		final, issueErr = auth.IssueBrowserSession(
			ctx,
			repo,
			auth.BrowserSessionIssue{
				UserID:    user.ID,
				IPAddress: "127.0.0.1",
				UserAgent: "session-test",
				Audit:     func(db.Session) {},
			},
		)
		return issueErr
	})
	if err != nil {
		t.Fatalf("consume challenge: %v", err)
	}

	// Then
	if final.ID == pending.Token {
		t.Fatal("final session reused the pending challenge token")
	}
	if final.CsrfToken == pending.CSRF {
		t.Fatal("final session reused the pending challenge CSRF token")
	}
	if !final.ReauthenticatedAt.Valid {
		t.Fatal("final session is missing reauthenticated_at")
	}
	if _, err := repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID: final.ID,
		},
	); err != nil {
		t.Fatalf("get final session: %v", err)
	}
	if _, err := repo.Queries.GetSession(
		context.Background(),
		db.GetSessionParams{
			ID: pending.Token,
		},
	); err != sql.ErrNoRows {
		t.Fatalf("pending token became a session: %v", err)
	}
	updated, err := repo.Queries.GetUserByID(context.Background(), user.ID)
	if err != nil {
		t.Fatalf("get updated user: %v", err)
	}
	if !updated.LastLoginAt.Valid {
		t.Fatal("final session did not update last_login_at")
	}
	if err := challenges.Consume(context.Background(), mfa.ChallengeBinding{
		Token:   pending.Token,
		CSRF:    pending.CSRF,
		UserID:  user.ID,
		Purpose: mfa.ChallengePurposeTOTPVerify,
	}, func(context.Context, db.MfaChallenge) error {
		return nil
	}); !errors.Is(err, mfa.ErrChallengeInvalid) {
		t.Fatalf("replayed challenge was accepted: %v", err)
	}
}

func TestRequireRecentAuth_rejects_session_older_than_five_minutes(
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
		t.Fatalf("issue session: %v", err)
	}
	if err := repo.Queries.MarkSessionReauthenticated(
		context.Background(), db.MarkSessionReauthenticatedParams{
			ID: session.ID,
			ReauthenticatedAt: sql.NullInt64{
				Int64: time.Now().Add(-6 * time.Minute).Unix(),
				Valid: true,
			},
		},
	); err != nil {
		t.Fatalf("mark session stale: %v", err)
	}
	handler := auth.AuthMiddleware(
		repo,
	)(
		auth.RequireRecentAuth(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		)),
	)
	req := httptest.NewRequest(http.MethodGet, "/settings/security", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: session.ID})
	rec := httptest.NewRecorder()

	// When
	handler.ServeHTTP(rec, req)

	// Then
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusSeeOther)
	}
	if location := rec.Header().
		Get("Location"); location != "/settings/security/reauth" {
		t.Fatalf("location = %q, want reauth route", location)
	}
}

func TestRequireRecentAuth_allows_fresh_session(t *testing.T) {
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
		t.Fatalf("issue session: %v", err)
	}
	handler := auth.AuthMiddleware(
		repo,
	)(
		auth.RequireRecentAuth(http.HandlerFunc(
			func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) },
		)),
	)
	req := httptest.NewRequest(http.MethodGet, "/settings/security", nil)
	req.AddCookie(&http.Cookie{Name: "session", Value: session.ID})
	rec := httptest.NewRecorder()

	// When
	handler.ServeHTTP(rec, req)

	// Then
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
}

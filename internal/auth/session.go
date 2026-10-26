package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

const (
	browserSessionTTL = 24 * time.Hour
	recentAuthTTL     = 5 * time.Minute
)

var ErrSessionAuditRequired = errors.New("final session audit is required")

var ErrPasswordChanged = errors.New("password changed during verification")

// BrowserSessionIssue holds trusted request metadata for a final browser login.
type BrowserSessionIssue struct {
	UserID    int64
	IPAddress string
	UserAgent string
	Audit     func(db.Session)
}

// PasswordChange holds a validated password update for one user.
type PasswordChange struct {
	UserID   int64
	Password string
	// ExpectedHash prevents a verified password from overwriting a newer change.
	ExpectedHash string
}

// IssueBrowserSession atomically creates a new final browser session and marks
// the user as recently logged in. Call it only after password-only login is
// complete or an MFA challenge has been consumed.
func IssueBrowserSession(
	ctx context.Context,
	repo *repository.Repository,
	issue BrowserSessionIssue,
) (db.Session, error) {
	var session db.Session
	if err := repo.WithTx(ctx, func(queries *db.Queries) error {
		created, err := IssueBrowserSessionWith(ctx, queries, issue)
		if err != nil {
			return err
		}
		session = created
		return nil
	}); err != nil {
		return db.Session{}, fmt.Errorf("issue browser session: %w", err)
	}
	issue.Audit(session)
	return session, nil
}

// IssueBrowserSessionWith creates a final browser session in a caller-owned
// transaction. The caller must invoke issue.Audit only after committing.
func IssueBrowserSessionWith(
	ctx context.Context,
	queries *db.Queries,
	issue BrowserSessionIssue,
) (db.Session, error) {
	if issue.Audit == nil {
		return db.Session{}, ErrSessionAuditRequired
	}
	token, csrf, err := NewSessionToken()
	if err != nil {
		return db.Session{}, fmt.Errorf(
			"generate browser session credentials: %w",
			err,
		)
	}
	now := time.Now().Unix()
	session, err := queries.CreateSession(ctx, db.CreateSessionParams{
		ID:        token,
		UserID:    issue.UserID,
		CsrfToken: csrf,
		ExpiresAt: now + int64(browserSessionTTL/time.Second),
		IpAddress: sql.NullString{
			String: issue.IPAddress,
			Valid:  issue.IPAddress != "",
		},
		UserAgent: sql.NullString{
			String: issue.UserAgent,
			Valid:  issue.UserAgent != "",
		},
	})
	if err != nil {
		return db.Session{}, fmt.Errorf("create browser session: %w", err)
	}
	if err := queries.UpdateUserLastLogin(ctx, db.UpdateUserLastLoginParams{
		ID:          issue.UserID,
		LastLoginAt: sql.NullInt64{Int64: now, Valid: true},
	}); err != nil {
		return db.Session{}, fmt.Errorf("update last login: %w", err)
	}
	return session, nil
}

// UpdatePassword changes a password and removes every browser-auth artifact
// for the user. API tokens deliberately remain valid.
func UpdatePassword(
	ctx context.Context,
	repo *repository.Repository,
	change PasswordChange,
) error {
	hash, err := HashPassword(change.Password)
	if err != nil {
		return fmt.Errorf("hash password: %w", err)
	}
	if err := repo.WithTx(ctx, func(queries *db.Queries) error {
		if change.ExpectedHash != "" {
			if _, err := queries.LockMFAUser(ctx, change.UserID); err != nil {
				return err
			}
			user, err := queries.GetUserByID(ctx, change.UserID)
			if err != nil {
				return err
			}
			if user.PasswordHash != change.ExpectedHash {
				return ErrPasswordChanged
			}
		}
		if err := queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
			ID:           change.UserID,
			PasswordHash: hash,
		}); err != nil {
			return fmt.Errorf("update password: %w", err)
		}
		return InvalidateBrowserAuthInTx(ctx, queries, change.UserID)
	}); err != nil {
		return fmt.Errorf("change password: %w", err)
	}
	return nil
}

// UpdateAccount commits profile, optional password, and browser-auth changes
// together. Callers validate the profile and password before calling it.
// A blank password keeps the current hash. API tokens remain valid.
func UpdateAccount(
	ctx context.Context,
	repo *repository.Repository,
	profile db.UpdateUserParams,
	password string,
) (db.User, error) {
	var hash string
	if password != "" {
		var err error
		hash, err = HashPassword(password)
		if err != nil {
			return db.User{}, fmt.Errorf("hash password: %w", err)
		}
	}
	var user db.User
	if err := repo.WithLocalAdministrationTx(ctx, func(queries *db.Queries) error {
		locked, err := queries.LockMFAUser(ctx, profile.ID)
		if err != nil {
			return fmt.Errorf("lock account: %w", err)
		}
		if locked == 0 {
			return sql.ErrNoRows
		}
		current, err := queries.GetUserByID(ctx, profile.ID)
		if err != nil {
			return err
		}
		if profile.Role != "admin" {
			if err := protectLastAdministrator(ctx, queries, current); err != nil {
				return err
			}
		}
		if err := queries.UpdateUser(ctx, profile); err != nil {
			return fmt.Errorf("update profile: %w", err)
		}
		if hash != "" {
			if err := queries.UpdateUserPassword(ctx, db.UpdateUserPasswordParams{
				ID: profile.ID, PasswordHash: hash,
			}); err != nil {
				return fmt.Errorf("update password: %w", err)
			}
		}
		if hash != "" || current.Role != profile.Role {
			if err := InvalidateBrowserAuthInTx(ctx, queries, profile.ID); err != nil {
				return err
			}
		}
		user, err = queries.GetUserByID(ctx, profile.ID)
		return err
	}); err != nil {
		return db.User{}, fmt.Errorf("update account: %w", err)
	}
	return user, nil
}

// InvalidateBrowserAuth removes browser sessions and pending MFA challenges.
// It intentionally does not touch API token rows.
func InvalidateBrowserAuth(
	ctx context.Context,
	repo *repository.Repository,
	userID int64,
) error {
	if err := repo.WithTx(ctx, func(queries *db.Queries) error {
		return InvalidateBrowserAuthInTx(ctx, queries, userID)
	}); err != nil {
		return fmt.Errorf("invalidate browser authentication: %w", err)
	}
	return nil
}

// InvalidateBrowserAuthInTx removes browser-auth state inside a caller-owned
// transaction. It intentionally leaves API token rows untouched.
func InvalidateBrowserAuthInTx(
	ctx context.Context,
	queries *db.Queries,
	userID int64,
) error {
	if _, err := queries.DeleteMFAChallengesByUserID(ctx, userID); err != nil {
		return fmt.Errorf("delete MFA challenges: %w", err)
	}
	if err := queries.DeleteSessionsByUser(ctx, userID); err != nil {
		return fmt.Errorf("delete browser sessions: %w", err)
	}
	return nil
}

// RequireRecentAuth redirects requests with an absent, stale, or invalid
// browser-session freshness timestamp to the reauthentication step.
func RequireRecentAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !IsRecentlyAuthenticated(SessionFromContext(r.Context())) {
			http.Redirect(
				w,
				r,
				"/settings/security/reauth",
				http.StatusSeeOther,
			)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func IsRecentlyAuthenticated(session *db.Session) bool {
	return session != nil && session.ReauthenticatedAt.Valid &&
		time.Since(
			time.Unix(session.ReauthenticatedAt.Int64, 0),
		) < recentAuthTTL
}

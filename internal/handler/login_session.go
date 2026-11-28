package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/requestmeta"
)

type finalLoginFactor string

const (
	finalLoginPassword finalLoginFactor = "password"
	finalLoginTOTP     finalLoginFactor = "totp"
	finalLoginRecovery finalLoginFactor = "recovery"
	finalLoginPasskey  finalLoginFactor = "passkey"
	finalLoginOIDC     finalLoginFactor = "oidc"
)

type finalSessionIssue struct {
	UserID int64
	Factor finalLoginFactor
}

func (h *AuthHandler) completePasswordLogin(
	r *http.Request,
	userID int64,
	verifiedHash string,
) (db.Session, mfa.PendingChallenge, error) {
	ctx := r.Context()
	issue := h.finalBrowserSessionIssue(r, finalSessionIssue{
		UserID: userID, Factor: finalLoginPassword,
	})
	var session db.Session
	var pending mfa.PendingChallenge
	err := h.repo.WithTx(ctx, func(queries *db.Queries) error {
		// Password and factor changes take this lock before revocation.
		locked, err := queries.LockMFAUser(ctx, userID)
		if err != nil {
			return fmt.Errorf("lock password login user: %w", err)
		}
		if locked != 1 {
			return auth.ErrPasswordChanged
		}
		user, err := queries.GetUserByID(ctx, userID)
		if errors.Is(err, sql.ErrNoRows) {
			return auth.ErrPasswordChanged
		}
		if err != nil {
			return err
		}
		if user.PasswordHash == "" || user.PasswordHash != verifiedHash {
			return auth.ErrPasswordChanged
		}
		factors, err := queries.CountMFAFactors(ctx, userID)
		if err != nil {
			return err
		}
		if factors > 0 {
			pending, err = h.challengeService().IssueWith(
				ctx, queries, mfa.ChallengeIssue{
					UserID: userID, Purpose: mfa.ChallengePurposeLoginMFA,
				},
			)
			return err
		}
		session, err = auth.IssueBrowserSessionWith(ctx, queries, issue)
		return err
	})
	if err != nil {
		return db.Session{}, mfa.PendingChallenge{}, err
	}
	if session.ID != "" {
		issue.Audit(session)
	}
	return session, pending, nil
}

func (h *AuthHandler) issueFinalBrowserSession(
	w http.ResponseWriter,
	r *http.Request,
	issue finalSessionIssue,
) error {
	finalIssue := h.finalBrowserSessionIssue(r, issue)
	session, err := auth.IssueBrowserSession(r.Context(), h.repo, finalIssue)
	if err != nil {
		return fmt.Errorf("issue final browser session: %w", err)
	}
	h.setFinalBrowserSessionCookie(w, session)
	return nil
}

func (h *AuthHandler) finalBrowserSessionIssue(
	r *http.Request,
	issue finalSessionIssue,
) auth.BrowserSessionIssue {
	ip := requestmeta.ClientIP(r)
	return auth.BrowserSessionIssue{
		UserID: issue.UserID, IPAddress: ip, UserAgent: r.UserAgent(),
		Audit: func(session db.Session) {
			if issue.Factor == finalLoginRecovery {
				audit.Record(r.Context(), h.repo, audit.Entry{
					UserID: sql.NullInt64{Int64: session.UserID, Valid: true},
					Action: "mfa_recovery_use", EntityType: "user",
					EntityID: sql.NullInt64{Int64: session.UserID, Valid: true},
					Details:  loginDetails(r, issue.Factor),
				})
			}
			action := "login"
			if issue.Factor != finalLoginPassword {
				action = "mfa_login_factor"
			}
			audit.Record(r.Context(), h.repo, audit.Entry{
				UserID: sql.NullInt64{Int64: session.UserID, Valid: true},
				Action: action, EntityType: "user",
				EntityID: sql.NullInt64{Int64: session.UserID, Valid: true},
				Details:  loginDetails(r, issue.Factor),
			})
		},
	}
}

func (h *AuthHandler) setFinalBrowserSessionCookie(
	w http.ResponseWriter,
	session db.Session,
) {
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: session.ID, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: h.cookieSecure,
		Expires: time.Unix(session.ExpiresAt, 0),
	})
}

package handler

import (
	"database/sql"
	"fmt"
	"net/http"
	"strings"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
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
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
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

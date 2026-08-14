package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

const securityActionError = "Unable to update security settings"

func (h *AuthHandler) securityIdentity(
	r *http.Request,
) (*db.User, *db.Session, bool) {
	user := auth.UserFromContext(r.Context())
	session := auth.SessionFromContext(r.Context())
	return user, session, user != nil && session != nil
}

func (h *AuthHandler) requireFreshSecuritySession(
	w http.ResponseWriter,
	r *http.Request,
) (*db.User, *db.Session, bool) {
	setNoStore(w)
	user, session, ok := h.securityIdentity(r)
	if !ok || !auth.IsRecentlyAuthenticated(session) {
		audit.Suppress(r)
		http.Redirect(w, r, "/settings/security/reauth", http.StatusSeeOther)
		return nil, nil, false
	}
	return user, session, true
}

func (h *AuthHandler) markCurrentSessionReauthenticated(
	r *http.Request,
	session *db.Session,
) error {
	return h.repo.Queries.MarkSessionReauthenticated(
		r.Context(),
		db.MarkSessionReauthenticatedParams{
			ID: session.ID,
			ReauthenticatedAt: sql.NullInt64{
				Int64: time.Now().Unix(),
				Valid: true,
			},
		},
	)
}

func (h *AuthHandler) currentSecurityChallenge(
	r *http.Request,
	purpose mfa.ChallengePurpose,
) (mfa.ResolvedChallenge, error) {
	user, session, ok := h.securityIdentity(r)
	if !ok {
		return mfa.ResolvedChallenge{}, mfa.ErrChallengeInvalid
	}
	token, csrf, err := securityChallengeCredentials(r)
	if err != nil {
		return mfa.ResolvedChallenge{}, err
	}
	return h.challengeService().ResolveBound(r.Context(), mfa.ChallengeBinding{
		Token: token, CSRF: csrf, UserID: user.ID, SessionID: session.ID,
		Purpose: purpose,
	})
}

func securityChallengeCredentials(r *http.Request) (string, string, error) {
	token := r.Header.Get("X-MFA-Challenge")
	csrf := r.Header.Get("X-MFA-Challenge-CSRF")
	if token != "" && csrf != "" {
		return token, csrf, nil
	}
	if err := r.ParseForm(); err != nil {
		return "", "", mfa.ErrChallengeCSRF
	}
	token = r.PostFormValue("challenge_token")
	csrf = r.PostFormValue("challenge_csrf")
	if token == "" || csrf == "" {
		return "", "", mfa.ErrChallengeCSRF
	}
	return token, csrf, nil
}

func (h *AuthHandler) clearBrowserSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(0, 0), HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: h.cookieSecure,
	})
}

func securityErrorStatus(err error) int {
	if errors.Is(err, mfa.ErrChallengeCSRF) {
		return http.StatusForbidden
	}
	return http.StatusUnprocessableEntity
}

package handler

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type AuthHandler struct {
	repo             *repository.Repository
	mfaService       *mfa.Service
	cookieSecure     bool
	oidcDisplayName  string
	oidcProvider     *oidc.Provider
	oidcTransactions *oidc.TransactionStore
}

func NewAuthHandler(repo *repository.Repository) *AuthHandler {
	return &AuthHandler{repo: repo}
}

func (h *AuthHandler) SetMFAService(service *mfa.Service) {
	h.mfaService = service
	h.cookieSecure = service.CookieSecure()
}

func (h *AuthHandler) SetOIDCDisplayName(displayName string) {
	h.oidcDisplayName = displayName
}

func (h *AuthHandler) SetOIDCLogin(
	provider *oidc.Provider,
	transactions *oidc.TransactionStore,
) {
	h.oidcProvider = provider
	h.oidcTransactions = transactions
}

func (h *AuthHandler) LoginGet(w http.ResponseWriter, r *http.Request) {
	if err := pages.LoginPage(r.URL.Path, "", h.oidcDisplayName).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (h *AuthHandler) LoginOIDCGet(w http.ResponseWriter, r *http.Request) {
	setNoStore(w)
	if h.oidcProvider == nil || h.oidcTransactions == nil {
		http.NotFound(w, r)
		return
	}
	transaction, cookie, err := h.oidcTransactions.Start(
		r.Context(),
		oidc.TransactionRequest{Mode: oidc.TransactionModeLogin},
	)
	if err != nil {
		h.renderOIDCLoginUnavailable(w, r)
		return
	}
	location, err := h.oidcProvider.AuthorizationURL(r.Context(), transaction)
	if err != nil {
		h.renderOIDCLoginUnavailable(w, r)
		return
	}
	http.SetCookie(w, cookie)
	http.Redirect(w, r, location, http.StatusSeeOther)
}

func (h *AuthHandler) renderOIDCLoginUnavailable(
	w http.ResponseWriter,
	r *http.Request,
) {
	markSafeErrorResponse(w)
	w.WriteHeader(http.StatusServiceUnavailable)
	_ = pages.LoginPage(
		r.URL.Path,
		"Single sign-on is temporarily unavailable",
		h.oidcDisplayName,
	).Render(r.Context(), w)
}

func (h *AuthHandler) LoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	email := r.PostFormValue("email")
	password := r.PostFormValue("password")

	user, err := h.repo.Queries.GetUserByEmail(r.Context(), email)
	if err != nil || !auth.VerifyPassword(user.PasswordHash, password) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_ = pages.LoginPage(
			r.URL.Path,
			"Invalid email or password",
			h.oidcDisplayName,
		).
			Render(r.Context(), w)
		return
	}
	factors, err := h.repo.Queries.CountMFAFactors(r.Context(), user.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if factors > 0 {
		setNoStore(w)
		pending, err := h.challengeService().
			Issue(r.Context(), mfa.ChallengeIssue{
				UserID:  user.ID,
				Purpose: mfa.ChallengePurposeLoginMFA,
			})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		h.setPendingMFACookies(w, pending)
		http.Redirect(w, r, "/login/mfa", http.StatusSeeOther)
		return
	}

	if err := h.issueFinalBrowserSession(w, r, finalSessionIssue{
		UserID: user.ID,
		Factor: finalLoginPassword,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *AuthHandler) LogoutPost(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		// Attribute the logout to a user before deleting the session.
		// If the session is gone or expired, skip the audit entry —
		// there is no user to attribute it to.
		if sess, serr := h.repo.Queries.GetSession(
			r.Context(),
			db.GetSessionParams{
				ID:        cookie.Value,
				ExpiresAt: 0,
			},
		); serr == nil {
			audit.Record(r.Context(), h.repo, audit.Entry{
				UserID:     sql.NullInt64{Int64: sess.UserID, Valid: true},
				Action:     "logout",
				EntityType: "user",
				EntityID:   sql.NullInt64{Int64: sess.UserID, Valid: true},
				Details:    loginDetails(r, ""),
			})
			audit.Suppress(r)
		}
		_ = h.repo.Queries.DeleteSession(r.Context(), cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Expires:  time.Unix(0, 0),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   h.cookieSecure,
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// loginDetails returns a JSON string with IP + user agent for audit
// entries. It never includes form values (passwords, emails).
func loginDetails(r *http.Request, factor finalLoginFactor) string {
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	details := map[string]string{"ip": ip, "user_agent": r.UserAgent()}
	if factor != "" {
		details["factor"] = string(factor)
	}
	b, _ := json.Marshal(details)
	return string(b)
}

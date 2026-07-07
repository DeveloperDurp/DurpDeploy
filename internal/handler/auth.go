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
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

type AuthHandler struct {
	repo *repository.Repository
}

func NewAuthHandler(repo *repository.Repository) *AuthHandler {
	return &AuthHandler{repo: repo}
}

func (h *AuthHandler) LoginGet(w http.ResponseWriter, r *http.Request) {
	if err := pages.LoginPage(r.URL.Path, "").Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
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
		_ = pages.LoginPage(r.URL.Path, "Invalid email or password").Render(r.Context(), w)
		return
	}

	token, csrf, err := auth.NewSessionToken()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	expiresAt := time.Now().Add(24 * time.Hour).Unix()

	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	ua := r.UserAgent()

	_, err = h.repo.Queries.CreateSession(r.Context(), db.CreateSessionParams{
		ID:        token,
		UserID:    user.ID,
		CsrfToken: csrf,
		ExpiresAt: expiresAt,
		IpAddress: sql.NullString{String: ip, Valid: true},
		UserAgent: sql.NullString{String: ua, Valid: true},
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
		Expires:  time.Unix(expiresAt, 0),
	})

	_ = h.repo.Queries.UpdateUserLastLogin(r.Context(), db.UpdateUserLastLoginParams{
		LastLoginAt: sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
		ID:          user.ID,
	})

	// Audit successful login. Failed logins are deliberately NOT logged
	// (privacy / enumeration). The details JSON carries IP + user agent
	// only — never the password.
	audit.Record(r.Context(), h.repo, audit.Entry{
		UserID:     sql.NullInt64{Int64: user.ID, Valid: true},
		Action:     "login",
		EntityType: "user",
		EntityID:   sql.NullInt64{Int64: user.ID, Valid: true},
		Details:    loginDetails(r),
	})

	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *AuthHandler) LogoutPost(w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie("session")
	if err == nil {
		// Attribute the logout to a user before deleting the session.
		// If the session is gone or expired, skip the audit entry —
		// there is no user to attribute it to.
		if sess, serr := h.repo.Queries.GetSession(r.Context(), db.GetSessionParams{
			ID:        cookie.Value,
			ExpiresAt: 0,
		}); serr == nil {
			audit.Record(r.Context(), h.repo, audit.Entry{
				UserID:     sql.NullInt64{Int64: sess.UserID, Valid: true},
				Action:     "logout",
				EntityType: "user",
				EntityID:   sql.NullInt64{Int64: sess.UserID, Valid: true},
				Details:    loginDetails(r),
			})
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
	})

	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// loginDetails returns a JSON string with IP + user agent for audit
// entries. It never includes form values (passwords, emails).
func loginDetails(r *http.Request) string {
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	b, _ := json.Marshal(map[string]string{"ip": ip, "user_agent": r.UserAgent()})
	return string(b)
}

package handler

import (
	"net/http"
	"time"

	"durpdeploy/internal/mfa"
)

func (h *AuthHandler) setPendingMFACookies(
	w http.ResponseWriter,
	pending mfa.PendingChallenge,
) {
	expires := time.Now().Add(5 * time.Minute)
	for _, cookie := range []*http.Cookie{
		{
			Name: "mfa_pending", Value: pending.Token, Path: "/", HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Secure: h.cookieSecure, Expires: expires,
		},
		{
			Name: "mfa_csrf", Value: pending.CSRF, Path: "/", HttpOnly: true,
			SameSite: http.SameSiteLaxMode, Secure: h.cookieSecure, Expires: expires,
		},
	} {
		http.SetCookie(w, cookie)
	}
}

func (h *AuthHandler) clearPendingMFACookies(w http.ResponseWriter) {
	for _, name := range []string{"mfa_pending", "mfa_csrf"} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1, Expires: time.Unix(0, 0),
			HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: h.cookieSecure,
		})
	}
}

func pendingCSRF(r *http.Request) string {
	cookie, err := r.Cookie("mfa_csrf")
	if err != nil {
		return ""
	}
	return cookie.Value
}

func setNoStore(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-store")
}

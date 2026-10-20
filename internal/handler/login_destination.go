package handler

import (
	"encoding/base64"
	"net/http"
	"time"

	"durpdeploy/internal/auth"
)

const loginDestinationCookie = "login_destination"

func (h *AuthHandler) rememberLoginDestination(
	w http.ResponseWriter, r *http.Request,
) {
	destination := r.URL.Query().Get("next")
	if destination == "" {
		h.takeLoginDestination(w, r)
		return
	}
	destination = auth.SafeLoginDestination(destination)
	http.SetCookie(w, &http.Cookie{
		Name:  loginDestinationCookie,
		Value: base64.RawURLEncoding.EncodeToString([]byte(destination)),
		Path:  "/", HttpOnly: true, SameSite: http.SameSiteLaxMode,
		Secure: h.cookieSecure, MaxAge: 600,
		Expires: time.Now().Add(10 * time.Minute),
	})
}

func (h *AuthHandler) takeLoginDestination(
	w http.ResponseWriter, r *http.Request,
) string {
	cookie, err := r.Cookie(loginDestinationCookie)
	if err != nil {
		return "/"
	}
	http.SetCookie(w, &http.Cookie{
		Name: loginDestinationCookie, Value: "", Path: "/", MaxAge: -1,
		Expires: time.Unix(0, 0), HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: h.cookieSecure,
	})
	if len(cookie.Value) > 4096 {
		return "/"
	}
	value, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil {
		return "/"
	}
	return auth.SafeLoginDestination(string(value))
}

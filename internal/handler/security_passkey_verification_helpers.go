package handler

import (
	"encoding/json"
	"net/http"
	"time"

	"durpdeploy/internal/mfa"
	"durpdeploy/views/pages"
)

const (
	passkeyVerificationTokenCookie = "passkey_verification_token"
	passkeyVerificationCSRFCookie  = "passkey_verification_csrf"
	passkeyVerificationMaxAge      = 5 * time.Minute
)

func decodePasskeyVerification(
	ceremony string,
) (pendingPasskeyVerification, error) {
	var verification pendingPasskeyVerification
	if err := json.Unmarshal([]byte(ceremony), &verification); err != nil {
		return pendingPasskeyVerification{}, mfa.ErrChallengeInvalid
	}
	return verification, nil
}

func (h *AuthHandler) pendingPasskeyVerification(
	r *http.Request,
) (mfa.ResolvedChallenge, error) {
	user, session, ok := h.securityIdentity(r)
	if !ok {
		return mfa.ResolvedChallenge{}, mfa.ErrChallengeInvalid
	}
	token, csrf, err := passkeyVerificationCredentials(r)
	if err != nil {
		return mfa.ResolvedChallenge{}, err
	}
	return h.challengeService().ResolveBound(r.Context(), mfa.ChallengeBinding{
		Token:     token,
		CSRF:      csrf,
		UserID:    user.ID,
		SessionID: session.ID,
		Purpose:   mfa.ChallengePurposeWebAuthnAuth,
	})
}

func passkeyVerificationCredentials(r *http.Request) (string, string, error) {
	token, err := r.Cookie(passkeyVerificationTokenCookie)
	if err != nil {
		return "", "", mfa.ErrChallengeCSRF
	}
	csrf, err := r.Cookie(passkeyVerificationCSRFCookie)
	if err != nil {
		return "", "", mfa.ErrChallengeCSRF
	}
	return token.Value, csrf.Value, nil
}

func (h *AuthHandler) setPasskeyVerificationCookies(
	w http.ResponseWriter,
	binding mfa.ChallengeBinding,
) {
	for _, cookie := range []http.Cookie{
		{
			Name:     passkeyVerificationTokenCookie,
			Value:    binding.Token,
			Path:     "/settings/security/passkeys/test",
			MaxAge:   int(passkeyVerificationMaxAge / time.Second),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   h.cookieSecure,
		},
		{
			Name:     passkeyVerificationCSRFCookie,
			Value:    binding.CSRF,
			Path:     "/settings/security/passkeys/test",
			MaxAge:   int(passkeyVerificationMaxAge / time.Second),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   h.cookieSecure,
		},
	} {
		http.SetCookie(w, &cookie)
	}
}

func (h *AuthHandler) clearPasskeyVerificationCookies(w http.ResponseWriter) {
	for _, name := range []string{
		passkeyVerificationTokenCookie,
		passkeyVerificationCSRFCookie,
	} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Path:     "/settings/security/passkeys/test",
			MaxAge:   -1,
			Expires:  time.Unix(0, 0),
			HttpOnly: true,
			SameSite: http.SameSiteLaxMode,
			Secure:   h.cookieSecure,
		})
	}
}

func (h *AuthHandler) renderPasskeyVerificationPage(
	w http.ResponseWriter,
	r *http.Request,
	csrf string,
	state mfa.ResolvedChallenge,
	errorMsg string,
) {
	if err := pages.PasskeyVerificationPage(
		r.URL.Path,
		csrf,
		state.Binding.Token,
		state.Binding.CSRF,
		errorMsg,
	).Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"render passkey verification",
			http.StatusInternalServerError,
		)
	}
}

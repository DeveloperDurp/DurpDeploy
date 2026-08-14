package handler

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strings"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
	"durpdeploy/views/pages"
)

func (h *AuthHandler) SecurityPasskeyRenamePost(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, _, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	credentialID, name, err := securityPasskeyForm(r)
	if err != nil {
		h.writeFactorError(w, err)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeFactorError(w, err)
		return
	}
	if err := factors.RenameCredential(
		r.Context(),
		user.ID,
		credentialID,
		name,
	); err != nil {
		h.writeFactorError(w, err)
		return
	}
	h.clearBrowserSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (h *AuthHandler) SecurityPasskeyDeletePost(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, _, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	credentialID, err := securityCredentialID(r)
	if err != nil {
		h.writePasskeyDeleteError(w, mfa.ErrMFAFactorCredentialInvalid)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeFactorError(w, err)
		return
	}
	if err := factors.DeleteCredential(
		r.Context(),
		user.ID,
		credentialID,
	); err != nil {
		h.writePasskeyDeleteError(w, err)
		return
	}
	http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
}

func (h *AuthHandler) SecurityRecoveryRegeneratePost(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, session, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeFactorError(w, err)
		return
	}
	codes, err := factors.RegenerateRecovery(r.Context(), user.ID)
	if err != nil {
		h.writeFactorError(w, err)
		return
	}
	replacement, err := auth.IssueBrowserSession(
		r.Context(),
		h.repo,
		auth.BrowserSessionIssue{
			UserID: user.ID,
			Audit:  func(db.Session) {},
		},
	)
	if err != nil {
		h.writeFactorError(w, err)
		return
	}
	h.setFinalBrowserSessionCookie(w, replacement)
	if err := pages.RecoveryCodesPage(
		r.URL.Path,
		codes,
		session.CsrfToken,
		false,
	).
		Render(r.Context(), w); err != nil {
		http.Error(w, "render recovery codes", http.StatusInternalServerError)
	}
}

func (h *AuthHandler) SecurityDisablePost(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, _, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeFactorError(w, err)
		return
	}
	if err := factors.Disable(r.Context(), user.ID); err != nil {
		h.writeFactorError(w, err)
		return
	}
	h.clearBrowserSessionCookie(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func securityPasskeyForm(r *http.Request) ([]byte, string, error) {
	credentialID, err := securityCredentialID(r)
	if err != nil {
		return nil, "", err
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" {
		return nil, "", mfa.ErrMFAFactorOperation
	}
	return credentialID, name, nil
}

func securityCredentialID(r *http.Request) ([]byte, error) {
	if err := r.ParseForm(); err != nil {
		return nil, mfa.ErrMFAFactorOperation
	}
	encoded := r.PostFormValue("credential_id")
	credentialID, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(credentialID) == 0 {
		return nil, mfa.ErrMFAFactorOperation
	}
	return credentialID, nil
}

func (h *AuthHandler) writeFactorError(w http.ResponseWriter, err error) {
	http.Error(w, securityActionError, securityErrorStatus(err))
}

func (h *AuthHandler) writePasskeyDeleteError(
	w http.ResponseWriter,
	err error,
) {
	message := securityActionError
	switch {
	case errors.Is(err, mfa.ErrMFAFactorCredentialInvalid):
		message = "This passkey request is invalid. Refresh Security and try again."
	case errors.Is(err, mfa.ErrMFAFactorCredentialUnavailable):
		message = "This passkey is unavailable. Refresh Security and try again."
	case errors.Is(err, mfa.ErrMFAFactorRequired):
		message = "Keep another factor configured before deleting this passkey."
	}
	http.Error(w, message, securityErrorStatus(err))
}

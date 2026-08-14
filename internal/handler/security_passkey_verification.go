package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func (h *AuthHandler) SecurityPasskeyTestGet(
	w http.ResponseWriter,
	r *http.Request,
) {
	_, session, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	state, err := h.pendingPasskeyVerification(r)
	if err != nil {
		h.clearPasskeyVerificationCookies(w)
		h.writePasskeyError(w, err)
		return
	}
	h.renderPasskeyVerificationPage(w, r, session.CsrfToken, state, "")
}

func (h *AuthHandler) SecurityPasskeyTestBeginPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	_, _, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	state, err := h.currentSecurityChallenge(
		r,
		mfa.ChallengePurposeWebAuthnAuth,
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	verification, err := decodePasskeyVerification(state.CeremonyJSON)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(verification.Options); err != nil {
		h.writePasskeyError(w, err)
	}
}

func (h *AuthHandler) SecurityPasskeyTestFinishPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, _, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	state, err := h.currentSecurityChallenge(
		r,
		mfa.ChallengePurposeWebAuthnAuth,
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	verification, err := decodePasskeyVerification(state.CeremonyJSON)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	if h.mfaService == nil {
		h.writePasskeyError(w, mfa.ErrMFAFactorOperation)
		return
	}
	adapter, err := h.mfaService.WebAuthnAdapter()
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	identity, err := h.repo.Queries.GetWebAuthnUserByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	credential, err := adapter.FinishPendingAssertion(
		mfa.PendingAssertionBinding{
			User:       *user,
			Identity:   identity,
			Credential: verification.Credential,
			Session:    verification.Session,
			Request:    r,
		},
	)
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	params := verification.Credential
	params.SignCount = int64(credential.Authenticator.SignCount)
	if credential.Authenticator.CloneWarning {
		params.CloneWarning = 1
	}
	var activation mfa.CredentialActivation
	err = h.challengeService().ConsumeWith(r.Context(), state.Binding, func(
		ctx context.Context,
		queries *db.Queries,
		_ db.MfaChallenge,
	) error {
		var activationErr error
		activation, activationErr = factors.CreateCredentialWith(
			ctx,
			queries,
			params,
		)
		return activationErr
	})
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	h.clearBrowserSessionCookie(w)
	h.clearPasskeyVerificationCookies(w)
	w.Header().Set("Content-Type", "application/json")
	if len(activation.RecoveryCodes) == 0 {
		if err := json.NewEncoder(w).Encode(securityPasskeyFinishResponse{
			Redirect: "/login",
		}); err != nil {
			h.writePasskeyError(w, err)
		}
		return
	}
	if err := json.NewEncoder(w).Encode(securityPasskeyFinishResponse{
		RecoveryCodes: activation.RecoveryCodes,
	}); err != nil {
		h.writePasskeyError(w, err)
	}
}

func (h *AuthHandler) SecurityPasskeyTestCancelPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	_, _, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	state, err := h.currentSecurityChallenge(
		r,
		mfa.ChallengePurposeWebAuthnAuth,
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	if err := h.challengeService().
		Cancel(r.Context(), state.Binding); err != nil {
		h.writePasskeyError(w, err)
		return
	}
	h.clearPasskeyVerificationCookies(w)
	http.Redirect(w, r, "/settings/security", http.StatusSeeOther)
}

package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"

	"github.com/go-webauthn/webauthn/webauthn"
)

func (h *AuthHandler) SecurityReauthWebAuthnBegin(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	state, err := h.currentSecurityChallenge(r, mfa.ChallengePurposeTOTPVerify)
	if err != nil {
		h.writeSecurityChallengeError(w, err)
		return
	}
	if h.mfaService == nil {
		h.writeSecurityChallengeError(w, mfa.ErrMFAFactorOperation)
		return
	}
	adapter, err := h.mfaService.WebAuthnAdapter()
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	user, err := h.repo.Queries.GetUserByID(r.Context(), state.Binding.UserID)
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
	credentials, err := h.repo.Queries.ListWebAuthnCredentialsByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	options, ceremony, err := adapter.BeginAssertion(
		user,
		identity,
		credentials,
	)
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	if _, err := h.challengeService().PromoteWebAuthn(
		r.Context(),
		state.Binding,
		*ceremony,
	); err != nil {
		h.writeSecurityChallengeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(options); err != nil {
		h.writeSecurityChallengeError(w, err)
	}
}

func (h *AuthHandler) SecurityReauthWebAuthnFinish(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	state, err := h.currentSecurityChallenge(
		r,
		mfa.ChallengePurposeWebAuthnAuth,
	)
	if err != nil {
		h.writeSecurityChallengeError(w, err)
		return
	}
	if h.mfaService == nil {
		h.writeSecurityChallengeError(w, mfa.ErrMFAFactorOperation)
		return
	}
	var ceremony webauthn.SessionData
	if err := json.Unmarshal(
		[]byte(state.CeremonyJSON),
		&ceremony,
	); err != nil {
		h.writeSecurityChallengeError(w, mfa.ErrChallengeInvalid)
		return
	}
	adapter, err := h.mfaService.WebAuthnAdapter()
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	user, err := h.repo.Queries.GetUserByID(r.Context(), state.Binding.UserID)
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
	credentials, err := h.repo.Queries.ListWebAuthnCredentialsByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	credential, err := adapter.FinishAssertion(mfa.AssertionBinding{
		User: user, Identity: identity, Rows: credentials, Session: ceremony, Request: r,
	})
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	cloneWarning := int64(0)
	if credential.Authenticator.CloneWarning {
		cloneWarning = 1
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeSecurityChallengeError(w, err)
		return
	}
	h.completeSecurityReauthentication(
		w,
		r,
		state.Binding,
		func(ctx context.Context, queries *db.Queries) error {
			_, err := factors.UpdateCredentialCounterWith(
				ctx,
				queries,
				credential.ID,
				int64(credential.Authenticator.SignCount),
				cloneWarning,
			)
			return err
		},
	)
}

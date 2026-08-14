package handler

import (
	"context"
	"encoding/json"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"

	"github.com/go-webauthn/webauthn/webauthn"
)

func (h *AuthHandler) LoginMFAWebAuthnBegin(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	state, err := h.pendingMFAChallenge(r, mfa.ChallengePurposeLoginMFA)
	if err != nil {
		h.writeMFAJSONError(w, r, err)
		return
	}
	availability, err := h.loginMFAAvailability(
		r.Context(),
		state.Binding.UserID,
	)
	if err != nil || !availability.Passkey {
		h.writeMFAJSONError(w, r, mfa.ErrMFAFactorOperation)
		return
	}
	if h.mfaService == nil {
		h.writeMFAJSONError(w, r, mfa.ErrMFAFactorOperation)
		return
	}
	adapter, err := h.mfaService.WebAuthnAdapter()
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	user, err := h.repo.Queries.GetUserByID(r.Context(), state.Binding.UserID)
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	identity, err := h.repo.Queries.GetWebAuthnUserByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	credentials, err := h.repo.Queries.ListWebAuthnCredentialsByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	options, session, err := adapter.BeginAssertion(user, identity, credentials)
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	if _, err := h.challengeService().
		PromoteWebAuthn(r.Context(), state.Binding, *session); err != nil {
		h.writeMFAJSONError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(options); err != nil {
		h.writeMFAJSONError(w, r, err)
	}
}

func (h *AuthHandler) LoginMFAWebAuthnFinish(
	w http.ResponseWriter,
	r *http.Request,
) {
	setNoStore(w)
	state, err := h.pendingMFAChallenge(r, mfa.ChallengePurposeWebAuthnAuth)
	if err != nil {
		h.writeMFAJSONError(w, r, err)
		return
	}
	availability, err := h.loginMFAAvailability(
		r.Context(),
		state.Binding.UserID,
	)
	if err != nil || !availability.Passkey {
		h.writeMFAJSONError(w, r, mfa.ErrMFAFactorOperation)
		return
	}
	if h.mfaService == nil {
		h.writeMFAJSONError(w, r, mfa.ErrMFAFactorOperation)
		return
	}
	var session webauthn.SessionData
	if err := json.Unmarshal([]byte(state.CeremonyJSON), &session); err != nil {
		h.writeMFAJSONError(w, r, mfa.ErrChallengeInvalid)
		return
	}
	adapter, err := h.mfaService.WebAuthnAdapter()
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	user, err := h.repo.Queries.GetUserByID(r.Context(), state.Binding.UserID)
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	identity, err := h.repo.Queries.GetWebAuthnUserByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	credentials, err := h.repo.Queries.ListWebAuthnCredentialsByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	credential, err := adapter.FinishAssertion(mfa.AssertionBinding{
		User: user, Identity: identity, Rows: credentials, Session: session, Request: r,
	})
	if err != nil {
		h.recordMFAFailureJSON(w, r, state.Binding)
		return
	}
	cloneWarning := int64(0)
	if credential.Authenticator.CloneWarning {
		cloneWarning = 1
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writeMFAJSONError(w, r, err)
		return
	}
	h.completeMFAJSON(w, r, mfaCompletion{
		Binding: state.Binding,
		Factor:  finalLoginPasskey,
		Mutate: func(ctx context.Context, queries *db.Queries) error {
			_, err := factors.UpdateCredentialCounterWith(
				ctx,
				queries,
				credential.ID,
				int64(credential.Authenticator.SignCount),
				cloneWarning,
			)
			return err
		},
	})
}

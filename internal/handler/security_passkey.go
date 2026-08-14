package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

type pendingPasskeyRegistration struct {
	Name    string               `json:"name"`
	Session webauthn.SessionData `json:"session"`
}

type securityPasskeyBeginResponse struct {
	CSRF    string                       `json:"csrf"`
	Options *protocol.CredentialCreation `json:"options"`
	Token   string                       `json:"token"`
}

type securityPasskeyFinishResponse struct {
	RecoveryCodes []string `json:"recovery_codes"`
	Redirect      string   `json:"redirect,omitempty"`
}

type pendingPasskeyVerification struct {
	Credential db.CreateWebAuthnCredentialParams `json:"credential"`
	Options    *protocol.CredentialAssertion     `json:"options"`
	Session    webauthn.SessionData              `json:"session"`
}

func (h *AuthHandler) SecurityPasskeyBeginPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, session, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		h.writePasskeyError(w, err)
		return
	}
	name := strings.TrimSpace(r.PostFormValue("name"))
	if name == "" {
		h.writePasskeyError(w, mfa.ErrMFAFactorOperation)
		return
	}
	credentials, err := h.repo.Queries.ListWebAuthnCredentialsByUserID(
		r.Context(),
		user.ID,
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	if len(credentials) > 0 {
		h.writePasskeyError(w, mfa.ErrMFAFactorOperation)
		return
	}
	if h.mfaService == nil {
		h.writePasskeyError(w, mfa.ErrMFAFactorOperation)
		return
	}
	adapter, err := h.mfaService.WebAuthnAdapter()
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	identity, err := factors.EnsureWebAuthnIdentity(
		r.Context(),
		user.ID,
		adapter.RPID(),
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	options, ceremony, err := adapter.BeginRegistration(
		*user,
		identity,
		credentials,
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	state, err := json.Marshal(pendingPasskeyRegistration{
		Name: name, Session: *ceremony,
	})
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	pending, err := h.challengeService().Issue(r.Context(), mfa.ChallengeIssue{
		UserID: user.ID, SessionID: session.ID,
		Purpose:      mfa.ChallengePurposeWebAuthnRegister,
		CeremonyJSON: string(state),
	})
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(securityPasskeyBeginResponse{
		CSRF: pending.CSRF, Options: options, Token: pending.Token,
	}); err != nil {
		h.writePasskeyError(w, err)
	}
}

func (h *AuthHandler) SecurityPasskeyFinishPost(
	w http.ResponseWriter,
	r *http.Request,
) {
	user, _, ok := h.requireFreshSecuritySession(w, r)
	if !ok {
		return
	}
	state, err := h.currentSecurityChallenge(
		r,
		mfa.ChallengePurposeWebAuthnRegister,
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	var pending pendingPasskeyRegistration
	if err := json.Unmarshal([]byte(state.CeremonyJSON), &pending); err != nil {
		h.writePasskeyError(w, mfa.ErrChallengeInvalid)
		return
	}
	if h.mfaService == nil {
		h.writePasskeyError(w, mfa.ErrMFAFactorOperation)
		return
	}
	adapter, err := h.mfaService.WebAuthnAdapter()
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	factors, err := h.mfaFactors()
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	identity, err := factors.EnsureWebAuthnIdentity(
		r.Context(),
		user.ID,
		adapter.RPID(),
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	credential, err := adapter.FinishRegistration(mfa.RegistrationBinding{
		User: *user, Identity: identity, Session: pending.Session, Request: r,
	})
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	params, err := mfa.CredentialToDB(credential, user.ID, pending.Name)
	if err != nil {
		h.recordSecurityFailure(w, r, state.Binding)
		return
	}
	options, assertionSession, err := adapter.BeginPendingAssertion(
		*user,
		identity,
		params,
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	ceremony, err := json.Marshal(pendingPasskeyVerification{
		Credential: params,
		Options:    options,
		Session:    *assertionSession,
	})
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	verification, err := h.challengeService().Promote(
		r.Context(),
		state.Binding,
		mfa.ChallengePurposeWebAuthnAuth,
		string(ceremony),
	)
	if err != nil {
		h.writePasskeyError(w, err)
		return
	}
	h.setPasskeyVerificationCookies(w, verification)
	http.Redirect(w, r, "/settings/security/passkeys/test", http.StatusSeeOther)
}

func (h *AuthHandler) writePasskeyError(w http.ResponseWriter, err error) {
	http.Error(w, securityActionError, securityErrorStatus(err))
}

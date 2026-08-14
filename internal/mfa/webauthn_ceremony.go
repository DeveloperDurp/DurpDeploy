package mfa

import (
	"bytes"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"durpdeploy/internal/db"
)

func (a *WebAuthnAdapter) BeginRegistration(
	user db.User,
	identity db.WebauthnUser,
	rows []db.WebauthnCredential,
) (*protocol.CredentialCreation, *webauthn.SessionData, error) {
	mapped, err := webauthnUserFromDB(
		user,
		identity,
		rows,
		a.webauthn.Config.RPID,
	)
	if err != nil {
		return nil, nil, err
	}

	return a.webauthn.BeginRegistration(
		mapped,
		webauthn.WithExclusions(
			webauthn.Credentials(mapped.credentials).CredentialDescriptors(),
		),
	)
}

func (a *WebAuthnAdapter) BeginAssertion(
	user db.User,
	identity db.WebauthnUser,
	rows []db.WebauthnCredential,
) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	mapped, err := webauthnUserFromDB(
		user,
		identity,
		rows,
		a.webauthn.Config.RPID,
	)
	if err != nil {
		return nil, nil, err
	}

	return a.webauthn.BeginLogin(mapped)
}

func (a *WebAuthnAdapter) BeginPendingAssertion(
	user db.User,
	identity db.WebauthnUser,
	credential db.CreateWebAuthnCredentialParams,
) (*protocol.CredentialAssertion, *webauthn.SessionData, error) {
	return a.BeginAssertion(user, identity, []db.WebauthnCredential{
		pendingCredential(credential),
	})
}

func (a *WebAuthnAdapter) ValidateAssertionBinding(
	binding AssertionBinding,
) error {
	if binding.Session.RelyingPartyID != a.webauthn.Config.RPID ||
		len(binding.Session.Challenge) == 0 ||
		binding.Session.UserVerification != protocol.VerificationRequired ||
		!bytes.Equal(binding.Session.UserID, binding.Identity.UserHandle) {
		return ErrWebAuthnAssertionRejected
	}
	if len(binding.Session.AllowedCredentialIDs) == 0 ||
		len(binding.Session.AllowedCredentialIDs) != len(binding.Rows) {
		return ErrWebAuthnAssertionRejected
	}

	mapped, err := webauthnUserFromDB(
		binding.User,
		binding.Identity,
		binding.Rows,
		a.webauthn.Config.RPID,
	)
	if err != nil {
		return ErrWebAuthnAssertionRejected
	}
	for _, allowedID := range binding.Session.AllowedCredentialIDs {
		found := false
		for _, credential := range mapped.credentials {
			if bytes.Equal(allowedID, credential.ID) {
				found = true
				break
			}
		}
		if !found {
			return ErrWebAuthnAssertionRejected
		}
	}

	return nil
}

func (a *WebAuthnAdapter) FinishRegistration(
	binding RegistrationBinding,
) (webauthn.Credential, error) {
	mapped, err := webauthnUserFromDB(
		binding.User,
		binding.Identity,
		nil,
		a.webauthn.Config.RPID,
	)
	if err != nil || binding.Session.RelyingPartyID != a.webauthn.Config.RPID ||
		len(binding.Session.Challenge) == 0 ||
		binding.Session.UserVerification != protocol.VerificationRequired ||
		!bytes.Equal(binding.Session.UserID, binding.Identity.UserHandle) ||
		binding.Request == nil {
		return webauthn.Credential{}, ErrWebAuthnRegistrationRejected
	}

	credential, err := a.webauthn.FinishRegistration(
		mapped,
		binding.Session,
		binding.Request,
	)
	if err != nil {
		return webauthn.Credential{}, ErrWebAuthnRegistrationRejected
	}
	return *credential, nil
}

func (a *WebAuthnAdapter) FinishAssertion(
	binding AssertionBinding,
) (webauthn.Credential, error) {
	if binding.Request == nil || a.ValidateAssertionBinding(binding) != nil {
		return webauthn.Credential{}, ErrWebAuthnAssertionRejected
	}

	mapped, err := webauthnUserFromDB(
		binding.User,
		binding.Identity,
		binding.Rows,
		a.webauthn.Config.RPID,
	)
	if err != nil {
		return webauthn.Credential{}, ErrWebAuthnAssertionRejected
	}
	credential, err := a.webauthn.FinishLogin(
		mapped,
		binding.Session,
		binding.Request,
	)
	if err != nil {
		return webauthn.Credential{}, ErrWebAuthnAssertionRejected
	}
	return *credential, nil
}

func (a *WebAuthnAdapter) FinishPendingAssertion(
	binding PendingAssertionBinding,
) (webauthn.Credential, error) {
	return a.FinishAssertion(AssertionBinding{
		User:     binding.User,
		Identity: binding.Identity,
		Rows: []db.WebauthnCredential{
			pendingCredential(binding.Credential),
		},
		Session: binding.Session,
		Request: binding.Request,
	})
}

func pendingCredential(
	credential db.CreateWebAuthnCredentialParams,
) db.WebauthnCredential {
	return db.WebauthnCredential{
		CredentialID:                  credential.CredentialID,
		UserID:                        credential.UserID,
		Name:                          credential.Name,
		PublicKey:                     credential.PublicKey,
		Aaguid:                        credential.Aaguid,
		TransportsJson:                credential.TransportsJson,
		Flags:                         credential.Flags,
		SignCount:                     credential.SignCount,
		CloneWarning:                  credential.CloneWarning,
		Attachment:                    credential.Attachment,
		AttestationType:               credential.AttestationType,
		AttestationFormat:             credential.AttestationFormat,
		AttestationClientDataJson:     credential.AttestationClientDataJson,
		AttestationClientDataHash:     credential.AttestationClientDataHash,
		AttestationAuthenticatorData:  credential.AttestationAuthenticatorData,
		AttestationPublicKeyAlgorithm: credential.AttestationPublicKeyAlgorithm,
		AttestationObject:             credential.AttestationObject,
	}
}

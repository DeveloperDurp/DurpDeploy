package mfa

import (
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"durpdeploy/internal/db"
)

var (
	ErrWebAuthnDisabled             = errors.New("webauthn is disabled")
	ErrWebAuthnIdentity             = errors.New("invalid webauthn identity")
	ErrWebAuthnCredential           = errors.New("invalid webauthn credential")
	ErrWebAuthnAssertionRejected    = errors.New("webauthn assertion rejected")
	ErrWebAuthnRegistrationRejected = errors.New(
		"webauthn registration rejected",
	)
)

type WebAuthnAdapter struct {
	webauthn *webauthn.WebAuthn
}

type AssertionBinding struct {
	User     db.User
	Identity db.WebauthnUser
	Rows     []db.WebauthnCredential
	Session  webauthn.SessionData
	Request  *http.Request
}

type RegistrationBinding struct {
	User     db.User
	Identity db.WebauthnUser
	Session  webauthn.SessionData
	Request  *http.Request
}

type PendingAssertionBinding struct {
	User       db.User
	Identity   db.WebauthnUser
	Credential db.CreateWebAuthnCredentialParams
	Session    webauthn.SessionData
	Request    *http.Request
}

func NewWebAuthnAdapter(config WebAuthnConfig) (*WebAuthnAdapter, error) {
	if !config.Enabled {
		return nil, ErrWebAuthnDisabled
	}
	if config.Origin == "" || config.RPID == "" {
		return nil, ErrWebAuthnIdentity
	}
	origin, err := url.Parse(config.Origin)
	if err != nil || origin.Scheme == "" || origin.Host == "" ||
		origin.User != nil || origin.Path != "" || origin.RawQuery != "" ||
		origin.ForceQuery || origin.Fragment != "" ||
		origin.Hostname() != config.RPID {
		return nil, ErrWebAuthnIdentity
	}

	instance, err := webauthn.New(&webauthn.Config{
		RPDisplayName:         "DurpDeploy",
		RPID:                  config.RPID,
		RPOrigins:             []string{config.Origin},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey:      protocol.ResidentKeyRequirementPreferred,
			UserVerification: protocol.VerificationRequired,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create webauthn adapter: %w", err)
	}

	return &WebAuthnAdapter{webauthn: instance}, nil
}

func NewWebAuthnUserIdentity(
	userID int64,
	rpID string,
) (db.CreateWebAuthnUserParams, error) {
	return newWebAuthnUserIdentity(rand.Reader, userID, rpID)
}

func newWebAuthnUserIdentity(
	random io.Reader,
	userID int64,
	rpID string,
) (db.CreateWebAuthnUserParams, error) {
	if userID <= 0 || rpID == "" {
		return db.CreateWebAuthnUserParams{}, ErrWebAuthnIdentity
	}

	handle := make([]byte, 32)
	if _, err := io.ReadFull(random, handle); err != nil {
		return db.CreateWebAuthnUserParams{}, fmt.Errorf(
			"generate webauthn user handle: %w",
			err,
		)
	}

	return db.CreateWebAuthnUserParams{
		UserID: userID, RpID: rpID, UserHandle: handle,
	}, nil
}

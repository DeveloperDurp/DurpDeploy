package mfa

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"

	"durpdeploy/internal/db"
)

type dbWebAuthnUser struct {
	id          []byte
	name        string
	displayName string
	credentials []webauthn.Credential
}

func CredentialFromDB(row db.WebauthnCredential) (webauthn.Credential, error) {
	if len(row.CredentialID) == 0 || len(row.PublicKey) == 0 || row.Flags < 0 ||
		row.Flags > math.MaxUint8 || row.SignCount < 0 ||
		row.SignCount > math.MaxUint32 ||
		(row.CloneWarning != 0 && row.CloneWarning != 1) {
		return webauthn.Credential{}, ErrWebAuthnCredential
	}

	transports := []protocol.AuthenticatorTransport{}
	if err := json.Unmarshal(
		[]byte(row.TransportsJson),
		&transports,
	); err != nil {
		return webauthn.Credential{}, fmt.Errorf(
			"decode credential transports: %w",
			ErrWebAuthnCredential,
		)
	}

	attachment := protocol.AuthenticatorAttachment("")
	if row.Attachment.Valid {
		attachment = protocol.AuthenticatorAttachment(row.Attachment.String)
		if attachment != protocol.Platform &&
			attachment != protocol.CrossPlatform {
			return webauthn.Credential{}, ErrWebAuthnCredential
		}
	}

	return webauthn.Credential{
		ID:        append([]byte(nil), row.CredentialID...),
		PublicKey: append([]byte(nil), row.PublicKey...),
		Transport: transports,
		Flags: webauthn.NewCredentialFlags(
			protocol.AuthenticatorFlags(row.Flags),
		),
		Authenticator: webauthn.Authenticator{
			AAGUID:       append([]byte(nil), row.Aaguid...),
			SignCount:    uint32(row.SignCount),
			CloneWarning: row.CloneWarning == 1,
			Attachment:   attachment,
		},
		AttestationType:   row.AttestationType,
		AttestationFormat: row.AttestationFormat,
		Attestation: webauthn.CredentialAttestation{
			ClientDataJSON: append(
				[]byte(nil),
				row.AttestationClientDataJson...),
			ClientDataHash: append(
				[]byte(nil),
				row.AttestationClientDataHash...),
			AuthenticatorData: append(
				[]byte(nil),
				row.AttestationAuthenticatorData...),
			PublicKeyAlgorithm: row.AttestationPublicKeyAlgorithm,
			Object:             append([]byte(nil), row.AttestationObject...),
		},
	}, nil
}

func CredentialToDB(
	credential webauthn.Credential,
	userID int64,
	name string,
) (db.CreateWebAuthnCredentialParams, error) {
	if userID <= 0 || name == "" || len(credential.ID) == 0 ||
		len(credential.PublicKey) == 0 {
		return db.CreateWebAuthnCredentialParams{}, ErrWebAuthnCredential
	}

	transports, err := json.Marshal(credential.Transport)
	if err != nil {
		return db.CreateWebAuthnCredentialParams{}, fmt.Errorf(
			"encode credential transports: %w",
			err,
		)
	}

	attachment := sql.NullString{}
	if credential.Authenticator.Attachment != "" {
		attachment = sql.NullString{
			String: string(credential.Authenticator.Attachment),
			Valid:  true,
		}
	}

	cloneWarning := int64(0)
	if credential.Authenticator.CloneWarning {
		cloneWarning = 1
	}

	return db.CreateWebAuthnCredentialParams{
		CredentialID: append([]byte(nil), credential.ID...),
		UserID:       userID,
		Name:         name,
		PublicKey: append(
			[]byte(nil),
			credential.PublicKey...),
		Aaguid: append(
			[]byte(nil),
			credential.Authenticator.AAGUID...),
		TransportsJson: string(transports),
		Flags:          int64(credential.Flags.ProtocolValue()),
		SignCount: int64(
			credential.Authenticator.SignCount,
		),
		CloneWarning:      cloneWarning,
		Attachment:        attachment,
		AttestationType:   credential.AttestationType,
		AttestationFormat: credential.AttestationFormat,
		AttestationClientDataJson: append(
			[]byte(nil),
			credential.Attestation.ClientDataJSON...),
		AttestationClientDataHash: append(
			[]byte(nil),
			credential.Attestation.ClientDataHash...),
		AttestationAuthenticatorData: append(
			[]byte(nil),
			credential.Attestation.AuthenticatorData...),
		AttestationPublicKeyAlgorithm: credential.Attestation.PublicKeyAlgorithm,
		AttestationObject: append(
			[]byte(nil),
			credential.Attestation.Object...),
	}, nil
}

func webauthnUserFromDB(
	user db.User,
	identity db.WebauthnUser,
	rows []db.WebauthnCredential,
	rpID string,
) (dbWebAuthnUser, error) {
	if user.ID <= 0 || user.ID != identity.UserID || identity.RpID != rpID ||
		len(identity.UserHandle) != 32 {
		return dbWebAuthnUser{}, ErrWebAuthnIdentity
	}

	credentials := make([]webauthn.Credential, len(rows))
	for i, row := range rows {
		if row.UserID != user.ID {
			return dbWebAuthnUser{}, ErrWebAuthnCredential
		}
		credential, err := CredentialFromDB(row)
		if err != nil {
			return dbWebAuthnUser{}, err
		}
		credentials[i] = credential
	}

	return dbWebAuthnUser{
		id:          append([]byte(nil), identity.UserHandle...),
		name:        user.Email,
		displayName: user.Name,
		credentials: credentials,
	}, nil
}

func (u dbWebAuthnUser) WebAuthnID() []byte {
	return u.id
}

func (u dbWebAuthnUser) WebAuthnName() string {
	return u.name
}

func (u dbWebAuthnUser) WebAuthnDisplayName() string {
	return u.displayName
}

func (u dbWebAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	return u.credentials
}

package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestMSSQL_MFASchemaPreservesBinaryFieldsAndRollsBack(t *testing.T) {
	// Given a native SQL Server database with a user and WebAuthn identity.
	ctx := context.Background()
	conn := newSQLServerTestDB(t)
	queries := db.New(conn)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "mssql-mfa-parity@example.com", PasswordHash: "hash", Name: "MFA Parity", Role: "admin",
	})
	requireNoError(t, err, "create user")
	_, err = queries.CreateWebAuthnUser(ctx, db.CreateWebAuthnUserParams{
		UserID: user.ID, RpID: "example.test", UserHandle: bytes.Repeat([]byte{1}, 32),
	})
	requireNoError(t, err, "create WebAuthn user")

	// When a credential with binary attestation fields is persisted.
	credentialID := []byte{2, 3, 4}
	publicKey := []byte{5, 6, 7}
	attestationObject := []byte{8, 9, 10}
	_, err = queries.CreateWebAuthnCredential(
		ctx,
		db.CreateWebAuthnCredentialParams{
			CredentialID:                  credentialID,
			UserID:                        user.ID,
			Name:                          "primary",
			PublicKey:                     publicKey,
			Aaguid:                        bytes.Repeat([]byte{11}, 16),
			TransportsJson:                "[]",
			AttestationType:               "none",
			AttestationFormat:             "none",
			AttestationClientDataJson:     []byte{12},
			AttestationClientDataHash:     []byte{13},
			AttestationAuthenticatorData:  []byte{14},
			AttestationPublicKeyAlgorithm: -7,
			AttestationObject:             attestationObject,
		},
	)
	requireNoError(t, err, "create WebAuthn credential")
	credential, err := queries.GetWebAuthnCredentialByID(ctx, credentialID)
	requireNoError(t, err, "get WebAuthn credential")
	if !bytes.Equal(credential.PublicKey, publicKey) ||
		!bytes.Equal(credential.AttestationObject, attestationObject) {
		t.Fatalf("credential binary fields = %#v", credential)
	}

	// Then a rollback leaves no row and user deletion cascades credential cleanup.
	tx, err := conn.BeginTx(ctx, nil)
	requireNoError(t, err, "begin transaction")
	rolledBackCredentialID := []byte{15}
	_, err = queries.WithTx(tx).
		CreateWebAuthnCredential(ctx, db.CreateWebAuthnCredentialParams{
			CredentialID: rolledBackCredentialID, UserID: user.ID, Name: "rolled-back", PublicKey: []byte{16}, TransportsJson: "[]",
		})
	requireNoError(t, err, "create transaction credential")
	requireNoError(t, tx.Rollback(), "rollback transaction")
	if _, err := queries.GetWebAuthnCredentialByID(
		ctx,
		rolledBackCredentialID,
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf(
			"get rolled-back credential error = %v, want sql.ErrNoRows",
			err,
		)
	}
	_, err = conn.ExecContext(ctx, "DELETE FROM users WHERE id = ?", user.ID)
	requireNoError(t, err, "delete user")
	if _, err := queries.GetWebAuthnCredentialByID(
		ctx,
		credentialID,
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("get cleaned credential error = %v, want sql.ErrNoRows", err)
	}
}

package db_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestMFASchema_PreservesBinaryCredentialsAndRollsBack(t *testing.T) {
	// Given a migrated SQLite database and one browser session.
	ctx := context.Background()
	queries, conn := newAuthTestDB(t)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "mfa-parity@example.com",
		PasswordHash: "hash",
		Name:         "MFA Parity",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = queries.CreateSession(ctx, db.CreateSessionParams{
		ID:        "mfa-parity-session",
		UserID:    user.ID,
		CsrfToken: "csrf",
		ExpiresAt: 1_700_003_600,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// When binary WebAuthn fields and session freshness are persisted.
	userHandle := bytes.Repeat([]byte{1}, 32)
	credentialID := []byte{2, 3, 4}
	publicKey := []byte{5, 6, 7}
	attestationObject := []byte{8, 9, 10}
	_, err = queries.CreateWebAuthnUser(ctx, db.CreateWebAuthnUserParams{
		UserID: user.ID, RpID: "example.test", UserHandle: userHandle,
	})
	if err != nil {
		t.Fatalf("create WebAuthn user: %v", err)
	}
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
	if err != nil {
		t.Fatalf("create WebAuthn credential: %v", err)
	}
	if err := queries.MarkSessionReauthenticated(
		ctx,
		db.MarkSessionReauthenticatedParams{
			ID:                "mfa-parity-session",
			ReauthenticatedAt: sql.NullInt64{Int64: 1_700_000_001, Valid: true},
		},
	); err != nil {
		t.Fatalf("mark session reauthenticated: %v", err)
	}

	// Then the exact binary fields and freshness timestamp round-trip.
	credential, err := queries.GetWebAuthnCredentialByID(ctx, credentialID)
	if err != nil {
		t.Fatalf("get WebAuthn credential: %v", err)
	}
	if !bytes.Equal(credential.PublicKey, publicKey) ||
		!bytes.Equal(credential.AttestationObject, attestationObject) {
		t.Fatalf("credential binary fields = %#v", credential)
	}
	session, err := queries.GetSession(ctx, db.GetSessionParams{
		ID: "mfa-parity-session", ExpiresAt: 1_700_000_000,
	})
	if err != nil || !session.ReauthenticatedAt.Valid ||
		session.ReauthenticatedAt.Int64 != 1_700_000_001 {
		t.Fatalf("reauthenticated session = %#v, error = %v", session, err)
	}

	// When a credential insert is rolled back, no partial row survives.
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	rolledBackCredentialID := []byte{15}
	_, err = queries.WithTx(tx).
		CreateWebAuthnCredential(ctx, db.CreateWebAuthnCredentialParams{
			CredentialID:   rolledBackCredentialID,
			UserID:         user.ID,
			Name:           "rolled-back",
			PublicKey:      []byte{16},
			TransportsJson: "[]",
		})
	if err != nil {
		t.Fatalf("create transaction credential: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}

	// Then rollback and user cleanup remove their dependent artifacts.
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
	if _, err := conn.ExecContext(
		ctx,
		"DELETE FROM users WHERE id = ?",
		user.ID,
	); err != nil {
		t.Fatalf("delete user: %v", err)
	}
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

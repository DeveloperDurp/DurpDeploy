package mfa

import (
	"bytes"
	"context"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

func TestWebAuthn_AttestationMetadataPersistsThroughGeneratedQueries(
	t *testing.T,
) {
	// Given: a migrated database and one MFA user.
	conn, err := migrate.Run(
		"file:" + t.TempDir() + "/mfa.db?_pragma=foreign_keys(1)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	queries := db.New(conn)
	user, err := queries.CreateUser(context.Background(), db.CreateUserParams{
		Email: "adapter@example.com", PasswordHash: "hash", Name: "Adapter", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// When: all durable v0.17.4 credential fields are stored and loaded.
	created, err := queries.CreateWebAuthnCredential(
		context.Background(),
		db.CreateWebAuthnCredentialParams{
			CredentialID: []byte{
				1,
			}, UserID: user.ID, Name: "key", PublicKey: []byte{2},
			TransportsJson: "[]", AttestationType: "none", AttestationFormat: "none",
			AttestationClientDataJson: []byte{
				3,
			}, AttestationClientDataHash: []byte{4},
			AttestationAuthenticatorData: []byte{
				5,
			}, AttestationPublicKeyAlgorithm: -7,
			AttestationObject: []byte{6},
		},
	)
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}
	loaded, err := queries.GetWebAuthnCredentialByID(
		context.Background(),
		created.CredentialID,
	)
	if err != nil {
		t.Fatalf("get credential: %v", err)
	}

	// Then: no raw attestation field is lost by generated persistence.
	if loaded.AttestationType != "none" || loaded.AttestationFormat != "none" ||
		!bytes.Equal(loaded.AttestationClientDataJson, []byte{3}) ||
		!bytes.Equal(loaded.AttestationClientDataHash, []byte{4}) ||
		!bytes.Equal(loaded.AttestationAuthenticatorData, []byte{5}) ||
		loaded.AttestationPublicKeyAlgorithm != -7 ||
		!bytes.Equal(loaded.AttestationObject, []byte{6}) {
		t.Fatal(
			"attestation metadata did not round-trip through generated queries",
		)
	}
}

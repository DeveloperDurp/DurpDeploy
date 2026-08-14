package migrate

import (
	"bytes"
	"context"
	"testing"

	"github.com/testcontainers/testcontainers-go/modules/postgres"

	generated "durpdeploy/internal/db"
)

func TestPostgres_WebAuthnCredentialCRUD(t *testing.T) {
	ctx := context.Background()
	ctr, err := postgres.Run(
		ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("could not start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = ctr.Terminate(context.Background()) })
	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	conn, err := Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	queries := generated.New(conn)
	var userID int64
	err = conn.QueryRow("INSERT INTO users (email, password_hash, name, role) VALUES (?, ?, ?, ?) RETURNING id", "postgres-mfa-crud@example.com", "hash", "MFA CRUD", "admin").
		Scan(&userID)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = queries.CreateWebAuthnUser(
		ctx,
		generated.CreateWebAuthnUserParams{
			UserID:     userID,
			RpID:       "example.test",
			UserHandle: bytes.Repeat([]byte{1}, 32),
		},
	)
	if err != nil {
		t.Fatalf("create WebAuthn user: %v", err)
	}
	credentialID := []byte{1, 2, 3}
	credential := generated.CreateWebAuthnCredentialParams{
		CredentialID:                  credentialID,
		UserID:                        userID,
		Name:                          "primary",
		PublicKey:                     []byte{4},
		Aaguid:                        bytes.Repeat([]byte{5}, 16),
		TransportsJson:                "[]",
		AttestationType:               "none",
		AttestationFormat:             "none",
		AttestationClientDataJson:     []byte{6},
		AttestationClientDataHash:     []byte{7},
		AttestationAuthenticatorData:  []byte{8},
		AttestationPublicKeyAlgorithm: -7,
		AttestationObject:             []byte{9},
	}
	_, err = queries.CreateWebAuthnCredential(ctx, credential)
	if err != nil {
		t.Fatalf("create WebAuthn credential: %v", err)
	}
	updated, err := queries.UpdateWebAuthnCredentialCounter(
		ctx,
		generated.UpdateWebAuthnCredentialCounterParams{
			CredentialID: credentialID,
			SignCount:    8,
			CloneWarning: 1,
		},
	)
	if err != nil || updated.SignCount != 8 || updated.CloneWarning != 1 {
		t.Fatalf("updated WebAuthn credential = %#v, error = %v", updated, err)
	}
	if err := queries.DeleteWebAuthnCredential(ctx, credentialID); err != nil {
		t.Fatalf("delete WebAuthn credential: %v", err)
	}
	if _, err := queries.CreateWebAuthnCredential(ctx, credential); err != nil {
		t.Fatalf("recreate directly deleted WebAuthn credential: %v", err)
	}
}

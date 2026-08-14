package mfa

import (
	"bytes"
	"context"
	"database/sql"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestFactorStore_ActivateTOTP_persistsCiphertextAndHashOnlyRows(
	t *testing.T,
) {
	// Given: a migrated user, a fixed TOTP seed, and the existing AES-GCM box.
	ctx := context.Background()
	queries, repo, conn, user := newFactorStoreFixture(t)
	box := newFactorBox(t, 0)
	store := NewFactorStore(FactorStoreConfig{Repository: repo, Box: box,
		TOTP: NewTOTP(
			func() time.Time { return time.Unix(1_700_000_000, 0) },
			nil,
		)})
	seed := "JBSWY3DPEHPK3PXP"

	// When: a confirmed first TOTP factor is activated.
	codes, err := store.ActivateTOTP(ctx, user.ID, seed, 10)
	if err != nil {
		t.Fatalf("activate TOTP: %v", err)
	}

	// Then: the database has ciphertext and hashes only, while ten codes are returned once.
	if len(codes) != recoveryCodeCount {
		t.Fatalf(
			"recovery code count = %d, want %d",
			len(codes),
			recoveryCodeCount,
		)
	}
	var encryptedSeed []byte
	if err := conn.QueryRowContext(ctx, "SELECT encrypted_seed FROM mfa_totp WHERE user_id = ?", user.ID).
		Scan(&encryptedSeed); err != nil {
		t.Fatalf("inspect encrypted TOTP seed: %v", err)
	}
	if bytes.Contains(encryptedSeed, []byte(seed)) {
		t.Fatal("TOTP seed was stored in plaintext")
	}
	rows, err := queries.ListUnusedRecoveryCodesByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("list recovery hashes: %v", err)
	}
	if len(rows) != recoveryCodeCount ||
		bytes.Equal(rows[0].CodeHash, []byte(codes[0])) {
		t.Fatal("recovery codes were not stored as hashes only")
	}
	wrongKeyStore := NewFactorStore(
		FactorStoreConfig{
			Repository: repo,
			Box:        newFactorBox(t, 1),
			TOTP:       NewTOTP(nil, nil),
		},
	)
	if _, err := wrongKeyStore.loadTOTP(ctx, user.ID); err == nil {
		t.Fatal("wrong encryption key loaded the stored seed")
	}
}

func TestFactorStore_DeleteFactor_keepsOneFactorDuringConcurrentLastDeletes(
	t *testing.T,
) {
	// Given: a user with exactly a TOTP factor and a WebAuthn credential.
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(
		FactorStoreConfig{
			Repository: repo,
			Box:        newFactorBox(t, 0),
			TOTP:       NewTOTP(nil, nil),
		},
	)
	if _, err := store.ActivateTOTP(
		ctx,
		user.ID,
		"JBSWY3DPEHPK3PXP",
		1,
	); err != nil {
		t.Fatalf("activate TOTP: %v", err)
	}
	credentialID := []byte{1}
	if _, err := store.CreateCredential(
		ctx,
		credentialParams(user.ID, credentialID),
	); err != nil {
		t.Fatalf("create credential: %v", err)
	}

	// When: each individual factor is deleted concurrently.
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		<-start
		results <- store.DeleteTOTP(ctx, user.ID)
	}()
	go func() {
		defer group.Done()
		<-start
		results <- store.DeleteCredential(ctx, user.ID, credentialID)
	}()
	close(start)
	group.Wait()
	close(results)

	// Then: only one deletion can succeed and MFA remains enabled.
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful concurrent deletions = %d, want 1", successes)
	}
	enabled, err := store.Enabled(ctx, user.ID)
	if err != nil {
		t.Fatalf("load enabled state: %v", err)
	}
	if !enabled {
		t.Fatal("concurrent deletion left MFA enabled with zero factors")
	}
}

func newFactorStoreFixture(
	t *testing.T,
) (*db.Queries, *repository.Repository, *sql.DB, db.User) {
	t.Helper()
	conn, err := migrate.Run(
		"file:" + t.TempDir() + "/factor.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	queries := db.New(conn)
	user, err := queries.CreateUser(context.Background(), db.CreateUserParams{
		Email: "factor@example.com", PasswordHash: "hash", Name: "Factor", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return queries, repository.New(conn), conn, user
}

func newFactorBox(t *testing.T, firstByte byte) *secret.Box {
	t.Helper()
	key := make([]byte, 32)
	key[0] = firstByte
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatalf("new secret box: %v", err)
	}
	return box
}

func credentialParams(
	userID int64,
	credentialID []byte,
) db.CreateWebAuthnCredentialParams {
	return db.CreateWebAuthnCredentialParams{
		CredentialID:      credentialID,
		UserID:            userID,
		Name:              "primary",
		PublicKey:         []byte{2},
		TransportsJson:    "[]",
		AttestationType:   "none",
		AttestationFormat: "none",
	}
}

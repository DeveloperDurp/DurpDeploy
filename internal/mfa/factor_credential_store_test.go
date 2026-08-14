package mfa

import (
	"context"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestFactorStore_DeleteCredentialRejectsEmptyCredential(t *testing.T) {
	// Given
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(FactorStoreConfig{
		Repository: repo, Box: newFactorBox(t, 0), TOTP: NewTOTP(nil, nil),
	})

	// When
	err := store.DeleteCredential(ctx, user.ID, nil)

	// Then
	if !errors.Is(err, ErrMFAFactorCredentialInvalid) {
		t.Fatalf("delete empty credential error = %v", err)
	}
}

func TestFactorStore_DeleteCredentialRejectsUnknownCredentialBeforeLastFactor(
	t *testing.T,
) {
	// Given
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(FactorStoreConfig{
		Repository: repo, Box: newFactorBox(t, 0), TOTP: NewTOTP(nil, nil),
	})
	if _, err := store.CreateCredential(
		ctx,
		credentialParams(user.ID, []byte("owned")),
	); err != nil {
		t.Fatalf("create owned credential: %v", err)
	}

	// When
	err := store.DeleteCredential(ctx, user.ID, []byte("unknown"))

	// Then
	if !errors.Is(err, ErrMFAFactorCredentialUnavailable) {
		t.Fatalf("delete unknown credential error = %v", err)
	}
}

func TestFactorStore_DeleteCredentialRejectsNotOwnedCredentialBeforeLastFactor(
	t *testing.T,
) {
	// Given
	ctx := context.Background()
	queries, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(FactorStoreConfig{
		Repository: repo, Box: newFactorBox(t, 0), TOTP: NewTOTP(nil, nil),
	})
	if _, err := store.CreateCredential(
		ctx,
		credentialParams(user.ID, []byte("owned")),
	); err != nil {
		t.Fatalf("create owned credential: %v", err)
	}
	other, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "other@example.com", PasswordHash: "hash", Name: "Other", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create other user: %v", err)
	}
	foreignCredentialID := []byte("foreign")
	if _, err := queries.CreateWebAuthnCredential(
		ctx,
		credentialParams(other.ID, foreignCredentialID),
	); err != nil {
		t.Fatalf("create foreign credential: %v", err)
	}

	// When
	err = store.DeleteCredential(ctx, user.ID, foreignCredentialID)

	// Then
	if !errors.Is(err, ErrMFAFactorCredentialUnavailable) {
		t.Fatalf("delete foreign credential error = %v", err)
	}
}

func TestFactorStore_DeleteCredentialRejectsLastOwnedCredential(t *testing.T) {
	// Given
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(FactorStoreConfig{
		Repository: repo, Box: newFactorBox(t, 0), TOTP: NewTOTP(nil, nil),
	})
	credentialID := []byte("only")
	if _, err := store.CreateCredential(
		ctx,
		credentialParams(user.ID, credentialID),
	); err != nil {
		t.Fatalf("create only credential: %v", err)
	}

	// When
	err := store.DeleteCredential(ctx, user.ID, credentialID)

	// Then
	if !errors.Is(err, ErrMFAFactorRequired) {
		t.Fatalf("delete last credential error = %v", err)
	}
}

func TestFactorStore_DeleteCredentialDeletesOwnedNonLastCredential(
	t *testing.T,
) {
	// Given
	ctx := context.Background()
	queries, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(FactorStoreConfig{
		Repository: repo, Box: newFactorBox(t, 0), TOTP: NewTOTP(nil, nil),
	})
	credentialID := []byte("deletable")
	if _, err := store.CreateCredential(
		ctx,
		credentialParams(user.ID, credentialID),
	); err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if _, err := store.ActivateTOTP(
		ctx,
		user.ID,
		"JBSWY3DPEHPK3PXP",
		1,
	); err != nil {
		t.Fatalf("activate second factor: %v", err)
	}

	// When
	err := store.DeleteCredential(ctx, user.ID, credentialID)

	// Then
	if err != nil {
		t.Fatalf("delete non-last credential: %v", err)
	}
	count, err := queries.CountMFAFactors(ctx, user.ID)
	if err != nil || count != 1 {
		t.Fatalf(
			"factor count after non-last deletion = %d, error %v",
			count,
			err,
		)
	}
}

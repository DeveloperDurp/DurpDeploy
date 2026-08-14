package mfa

import (
	"context"
	"errors"
	"sync"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestFactorStore_DisableRemovesMFABrowserStateButPreservesAPITokens(
	t *testing.T,
) {
	// Given
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(FactorStoreConfig{
		Repository: repo, Box: newFactorBox(t, 0), TOTP: NewTOTP(nil, nil),
	})
	if _, err := store.ActivateTOTP(
		ctx,
		user.ID,
		"JBSWY3DPEHPK3PXP",
		1,
	); err != nil {
		t.Fatalf("activate TOTP: %v", err)
	}
	seedBrowserStateForFactorInvalidation(t, repo, user)
	token, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint API token: %v", err)
	}
	if _, err := repo.Queries.CreateApiToken(ctx, db.CreateApiTokenParams{
		ID: "disable-store-token", UserID: user.ID, Name: "preserved",
		TokenPrefix: prefix, TokenHash: hash, Scope: "global",
	}); err != nil {
		t.Fatalf("create API token: %v", err)
	}

	// When
	err = store.Disable(ctx, user.ID)

	// Then
	if err != nil {
		t.Fatalf("disable MFA: %v", err)
	}
	assertBrowserStateInvalidated(t, repo, user)
	for _, table := range []string{"mfa_totp", "webauthn_credentials", "mfa_recovery_codes"} {
		var count int
		if err := repo.DB.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM "+table+" WHERE user_id = ?",
			user.ID,
		).Scan(&count); err != nil || count != 0 {
			t.Fatalf("disable state for %s = %d, error %v", table, count, err)
		}
	}
	var tokenCount int
	if err := repo.DB.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM api_tokens WHERE user_id = ?",
		user.ID,
	).Scan(&tokenCount); err != nil || token == "" || tokenCount != 1 {
		t.Fatalf("API token state = %d, error %v", tokenCount, err)
	}
}

func TestFactorStore_DisableWinsOverConcurrentIndividualDeletion(t *testing.T) {
	// Given
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(FactorStoreConfig{
		Repository: repo, Box: newFactorBox(t, 0), TOTP: NewTOTP(nil, nil),
	})
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
		t.Fatalf("create passkey: %v", err)
	}
	start := make(chan struct{})
	var group sync.WaitGroup
	group.Add(2)
	var disableErr error
	go func() {
		defer group.Done()
		<-start
		disableErr = store.Disable(ctx, user.ID)
	}()
	go func() {
		defer group.Done()
		<-start
		_ = store.DeleteCredential(ctx, user.ID, credentialID)
	}()

	// When
	close(start)
	group.Wait()

	// Then
	if disableErr != nil {
		t.Fatalf("concurrent disable failed: %v", disableErr)
	}
	enabled, err := store.Enabled(ctx, user.ID)
	if err != nil || enabled {
		t.Fatalf("concurrent disable left enabled=%t, error %v", enabled, err)
	}
}

func TestFactorStore_ResetRollsBackWhenRecoveryCleanupFails(t *testing.T) {
	// Given
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(FactorStoreConfig{
		Repository: repo, Box: newFactorBox(t, 0), TOTP: NewTOTP(nil, nil),
	})
	if _, err := store.ActivateTOTP(
		ctx,
		user.ID,
		"JBSWY3DPEHPK3PXP",
		1,
	); err != nil {
		t.Fatalf("activate TOTP: %v", err)
	}
	if _, err := store.CreateCredential(
		ctx,
		credentialParams(user.ID, []byte{8}),
	); err != nil {
		t.Fatalf("create passkey: %v", err)
	}
	seedBrowserStateForFactorInvalidation(t, repo, user)
	if _, err := repo.DB.ExecContext(ctx, `
		CREATE TRIGGER fail_mfa_reset_recovery
		BEFORE DELETE ON mfa_recovery_codes
		BEGIN
			SELECT RAISE(ABORT, 'forced reset rollback');
		END
	`); err != nil {
		t.Fatalf("create rollback trigger: %v", err)
	}

	// When
	err := store.Reset(ctx, user.ID)

	// Then
	if !errors.Is(err, ErrMFAFactorOperation) {
		t.Fatalf("reset error = %v, want factor operation error", err)
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM mfa_totp WHERE user_id = ?",
		"SELECT COUNT(*) FROM webauthn_credentials WHERE user_id = ?",
		"SELECT COUNT(*) FROM mfa_recovery_codes WHERE user_id = ?",
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?",
		"SELECT COUNT(*) FROM sessions WHERE user_id = ?",
	} {
		var count int
		if err := repo.DB.QueryRowContext(ctx, query, user.ID).
			Scan(&count); err != nil {
			t.Fatalf("count rollback state for %q: %v", query, err)
		}
		if count == 0 {
			t.Fatalf("reset rollback removed state for query %q", query)
		}
	}
}

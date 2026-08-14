package mfa

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestTOTPReplay_ConcurrentVerificationAllowsOneAcceptedStep(t *testing.T) {
	// Given: a confirmed seed and a code for a fixed 30-second step.
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	at := time.Unix(1_700_000_000, 0)
	seed := "JBSWY3DPEHPK3PXP"
	store := NewFactorStore(FactorStoreConfig{
		Repository: repo, Box: newFactorBox(t, 0),
		TOTP: NewTOTP(func() time.Time { return at }, nil),
	})
	if _, err := store.ActivateTOTP(ctx, user.ID, seed, -1); err != nil {
		t.Fatalf("activate TOTP: %v", err)
	}
	code, err := GenerateTOTPCode(seed, at)
	if err != nil {
		t.Fatalf("generate TOTP code: %v", err)
	}

	// When: two verifiers submit the same step concurrently.
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	for range 2 {
		go func() {
			defer group.Done()
			<-start
			results <- store.VerifyTOTP(ctx, user.ID, code)
		}()
	}
	close(start)
	group.Wait()
	close(results)

	// Then: the stored accepted step gives exactly one verifier ownership.
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful TOTP verifications = %d, want 1", successes)
	}
}

func TestRecoveryConsume_ConcurrentReuseAndRegenerationAreAtomic(t *testing.T) {
	// Given: first-factor recovery codes returned exactly once.
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(
		FactorStoreConfig{
			Repository: repo,
			Box:        newFactorBox(t, 0),
			TOTP:       NewTOTP(nil, nil),
		},
	)
	codes, err := store.ActivateTOTP(ctx, user.ID, "JBSWY3DPEHPK3PXP", 1)
	if err != nil {
		t.Fatalf("activate TOTP: %v", err)
	}
	if len(codes) != recoveryCodeCount {
		t.Fatalf(
			"first activation codes = %d, want %d",
			len(codes),
			recoveryCodeCount,
		)
	}

	// When: the same recovery code is consumed concurrently, then replaced.
	start := make(chan struct{})
	results := make(chan error, 2)
	var group sync.WaitGroup
	group.Add(2)
	for range 2 {
		go func() {
			defer group.Done()
			<-start
			results <- store.ConsumeRecovery(ctx, user.ID, codes[0], 1_700_000_000)
		}()
	}
	close(start)
	group.Wait()
	close(results)
	regenerated, err := store.RegenerateRecovery(ctx, user.ID)
	if err != nil {
		t.Fatalf("regenerate recovery codes: %v", err)
	}

	// Then: one reuse succeeds, replaced codes cannot be consumed, and the new set has ten codes.
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("successful recovery consumptions = %d, want 1", successes)
	}
	if err := store.ConsumeRecovery(
		ctx,
		user.ID,
		codes[1],
		1_700_000_001,
	); err == nil {
		t.Fatal("replaced recovery code was accepted")
	}
	if len(regenerated) != recoveryCodeCount {
		t.Fatalf(
			"regenerated codes = %d, want %d",
			len(regenerated),
			recoveryCodeCount,
		)
	}
}

func TestFactorStore_CreateCredential_rejectsDuplicateCredentialAndRetainsEnabledState(
	t *testing.T,
) {
	// Given: an enrolled TOTP user and a passkey registration payload.
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
	params := credentialParams(user.ID, []byte{7})
	if _, err := store.CreateCredential(ctx, params); err != nil {
		t.Fatalf("create credential: %v", err)
	}

	// When: the credential ID is registered again.
	_, err := store.CreateCredential(ctx, params)

	// Then: the duplicate fails without disabling either confirmed factor.
	if err == nil {
		t.Fatal("duplicate credential registration succeeded")
	}
	enabled, err := store.Enabled(ctx, user.ID)
	if err != nil {
		t.Fatalf("load enabled state: %v", err)
	}
	if !enabled {
		t.Fatal("duplicate credential failure changed enabled state")
	}
}

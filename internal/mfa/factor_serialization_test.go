package mfa

import (
	"bytes"
	"context"
	"sync"
	"testing"

	"durpdeploy/internal/db"
)

func TestFactorStore_ConcurrentFirstActivation_returnsRecoveryCodesOnce(
	t *testing.T,
) {
	// Given: one user starts without factors and two distinct factor activations.
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(
		FactorStoreConfig{
			Repository: repo,
			Box:        newFactorBox(t, 0),
			TOTP:       NewTOTP(nil, nil),
		},
	)
	start := make(chan struct{})
	totpResult := make(chan factorActivationResult, 1)
	credentialResult := make(chan factorActivationResult, 1)

	// When: TOTP and a credential activation begin at the same time.
	go func() {
		<-start
		codes, err := store.ActivateTOTP(ctx, user.ID, "JBSWY3DPEHPK3PXP", 1)
		totpResult <- factorActivationResult{codes: codes, err: err}
	}()
	go func() {
		<-start
		activation, err := store.CreateCredential(
			ctx,
			credentialParams(user.ID, []byte{8}),
		)
		credentialResult <- factorActivationResult{codes: activation.RecoveryCodes, err: err}
	}()
	close(start)
	totp := <-totpResult
	credential := <-credentialResult

	// Then: both factors persist, but only the first activation displays recovery codes.
	if totp.err != nil {
		t.Fatalf("activate TOTP: %v", totp.err)
	}
	if credential.err != nil {
		t.Fatalf("create credential: %v", credential.err)
	}
	if len(totp.codes)+len(credential.codes) != recoveryCodeCount {
		t.Fatalf(
			"displayed recovery codes = %d, want %d",
			len(totp.codes)+len(credential.codes),
			recoveryCodeCount,
		)
	}
}

func TestFactorStore_RegenerateRecovery_rollsBackReplacementOnInsertFailure(
	t *testing.T,
) {
	// Given: existing recovery rows and a second user that owns a future replacement hash.
	ctx := context.Background()
	queries, repo, _, user := newFactorStoreFixture(t)
	randomness := recoveryRandomness()
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
	original, err := queries.ListUnusedRecoveryCodesByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("list original recovery rows: %v", err)
	}
	futureCodes, err := GenerateRecoveryCodes(bytes.NewReader(randomness))
	if err != nil {
		t.Fatalf("generate collision code: %v", err)
	}
	futureHash, err := HashRecoveryCode(futureCodes[0])
	if err != nil {
		t.Fatalf("hash collision code: %v", err)
	}
	other, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "collision@example.com", PasswordHash: "hash", Name: "Collision", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create collision user: %v", err)
	}
	if _, err := queries.CreateRecoveryCode(ctx, db.CreateRecoveryCodeParams{
		ID: "collision", UserID: other.ID, CodeHash: futureHash[:],
	}); err != nil {
		t.Fatalf("create collision recovery row: %v", err)
	}
	store.recoveryRandom = bytes.NewReader(randomness)

	// When: regeneration encounters the globally unique hash after deleting old rows.
	_, err = store.RegenerateRecovery(ctx, user.ID)

	// Then: the transaction rolls back and every original row remains.
	if err == nil {
		t.Fatal("regeneration with a duplicate hash succeeded")
	}
	remaining, err := queries.ListUnusedRecoveryCodesByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("list recovered rows after rollback: %v", err)
	}
	if len(remaining) != len(original) {
		t.Fatalf(
			"recovery rows after rollback = %d, want %d",
			len(remaining),
			len(original),
		)
	}
}

func TestFactorStore_CredentialLoadAndCounterUpdate_roundTrip(t *testing.T) {
	// Given: one persisted credential.
	ctx := context.Background()
	_, repo, _, user := newFactorStoreFixture(t)
	store := NewFactorStore(
		FactorStoreConfig{
			Repository: repo,
			Box:        newFactorBox(t, 0),
			TOTP:       NewTOTP(nil, nil),
		},
	)
	credentialID := []byte{9}
	created, err := store.CreateCredential(
		ctx,
		credentialParams(user.ID, credentialID),
	)
	if err != nil {
		t.Fatalf("create credential: %v", err)
	}

	// When: the credential is loaded and its assertion counter is advanced.
	loaded, err := store.LoadCredential(ctx, credentialID)
	if err != nil {
		t.Fatalf("load credential: %v", err)
	}
	updated, err := store.UpdateCredentialCounter(ctx, credentialID, 7, 1)
	if err != nil {
		t.Fatalf("update credential counter: %v", err)
	}

	// Then: the persisted identity and updated counter are returned.
	if !bytes.Equal(loaded.CredentialID, created.Credential.CredentialID) {
		t.Fatal("loaded credential ID differs from created credential")
	}
	if updated.SignCount != 7 || updated.CloneWarning != 1 {
		t.Fatalf("updated credential = %#v", updated)
	}
}

func TestFactorStore_ConcurrentRegeneration_persistsOneCompleteReplacement(
	t *testing.T,
) {
	// Given: a user with recovery codes from a confirmed first factor.
	ctx := context.Background()
	queries, repo, _, user := newFactorStoreFixture(t)
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
	start := make(chan struct{})
	results := make(chan factorActivationResult, 2)
	var group sync.WaitGroup
	group.Add(2)

	// When: two replacement requests begin at the same time.
	for range 2 {
		go func() {
			defer group.Done()
			<-start
			codes, err := store.RegenerateRecovery(ctx, user.ID)
			results <- factorActivationResult{codes: codes, err: err}
		}()
	}
	close(start)
	group.Wait()
	close(results)

	// Then: persisted hashes exactly match one whole successful replacement set.
	var replacements [][]string
	for result := range results {
		if result.err != nil {
			t.Fatalf("regenerate recovery codes: %v", result.err)
		}
		replacements = append(replacements, result.codes)
	}
	rows, err := queries.ListUnusedRecoveryCodesByUserID(ctx, user.ID)
	if err != nil {
		t.Fatalf("list regenerated recovery rows: %v", err)
	}
	if len(rows) != recoveryCodeCount {
		t.Fatalf(
			"persisted recovery rows = %d, want %d",
			len(rows),
			recoveryCodeCount,
		)
	}
	if !matchesRecoveryReplacement(rows, replacements[0]) &&
		!matchesRecoveryReplacement(rows, replacements[1]) {
		t.Fatal("persisted recovery hashes do not match a complete replacement")
	}
}

type factorActivationResult struct {
	codes []string
	err   error
}

func recoveryRandomness() []byte {
	raw := make([]byte, recoveryCodeCount*recoveryCodeBytes)
	for index := range raw {
		raw[index] = byte(index + 1)
	}
	return raw
}

func matchesRecoveryReplacement(
	rows []db.MfaRecoveryCode,
	codes []string,
) bool {
	if len(rows) != len(codes) {
		return false
	}
	hashes := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		hashes[string(row.CodeHash)] = struct{}{}
	}
	for _, code := range codes {
		hash, err := HashRecoveryCode(code)
		if err != nil {
			return false
		}
		if _, found := hashes[string(hash[:])]; !found {
			return false
		}
	}
	return true
}

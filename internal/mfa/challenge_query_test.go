package mfa

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

func TestChallenge_GuardedConsumeAllowsOneWinnerWhenConcurrent(t *testing.T) {
	// Given: one bound challenge and two guarded consumers released together.
	ctx := context.Background()
	conn, err := migrate.Run(
		"file:" + t.TempDir() + "/mfa.db?_pragma=foreign_keys(1)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetMaxOpenConns(1)
	queries := db.New(conn)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "guarded@example.test", PasswordHash: "hash", Name: "Guarded", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	tokenHash := make([]byte, 32)
	csrfHash := make([]byte, 32)
	csrfHash[0] = 1
	if _, err := queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: tokenHash, UserID: user.ID, Purpose: "totp_verify", CsrfHash: csrfHash,
		CeremonyJson: "{}", ExpiresAt: 1_700_000_300,
	}); err != nil {
		t.Fatalf("create challenge: %v", err)
	}
	params := db.ConsumeMFAChallengeGuardedParams{
		TokenHash: tokenHash, UserID: user.ID, Purpose: "totp_verify",
		SessionID: sql.NullString{}, CsrfHash: csrfHash, ExpiresAt: 1_700_000_000,
		Attempts: 5,
	}
	start := make(chan struct{})
	results := make(chan int64, 2)
	var group sync.WaitGroup

	// When: both consumers submit matching bindings at once.
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			rows, consumeErr := queries.ConsumeMFAChallengeGuarded(ctx, params)
			if consumeErr != nil {
				t.Errorf("guarded consume: %v", consumeErr)
				return
			}
			results <- rows
		}()
	}
	close(start)
	group.Wait()
	close(results)

	// Then: SQL grants the row to exactly one caller.
	var consumed int64
	for rows := range results {
		consumed += rows
	}
	if consumed != 1 {
		t.Fatalf("consumed rows = %d, want 1", consumed)
	}
}

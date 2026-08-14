package mfa

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go/modules/postgres"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

func TestPostgres_ChallengeGuardedConsume(t *testing.T) {
	// Given: a disposable PostgreSQL database.
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		postgres.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("PostgreSQL container unavailable: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL DSN: %v", err)
	}
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// When: guarded consume is exercised with NULL and bound session rows.
	assertGuardedConsumePortability(t, conn)
}

func TestMSSQL_ChallengeGuardedConsume(t *testing.T) {
	// Given: an explicitly configured SQL Server database.
	dsn := os.Getenv("DURPDEPLOY_MSSQL_TEST_DSN")
	if dsn == "" {
		t.Skip("DURPDEPLOY_MSSQL_TEST_DSN is not configured")
	}
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate SQL Server: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// When: guarded consume is exercised with NULL and bound session rows.
	assertGuardedConsumePortability(t, conn)
}

func assertGuardedConsumePortability(t *testing.T, conn *sql.DB) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(conn)
	stamp := time.Now().UnixNano()
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: fmt.Sprintf(
			"guarded-%d@example.test",
			stamp,
		), PasswordHash: "hash",
		Name: "Guarded", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	t.Cleanup(func() { _ = queries.DeleteUser(context.Background(), user.ID) })
	csrfHash := make([]byte, 32)
	csrfHash[0] = 1
	nullHash := make([]byte, 32)
	nullHash[0] = 2
	if _, err := queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: nullHash, UserID: user.ID, Purpose: "totp_verify", CsrfHash: csrfHash,
		CeremonyJson: "{}", ExpiresAt: 1_700_000_300,
	}); err != nil {
		t.Fatalf("create NULL-session challenge: %v", err)
	}
	nullParams := db.ConsumeMFAChallengeGuardedParams{
		TokenHash: nullHash, UserID: user.ID, Purpose: "totp_verify", CsrfHash: csrfHash,
		ExpiresAt: 1_700_000_000, Attempts: maxFailures,
	}
	consumed, err := queries.ConsumeMFAChallengeGuarded(ctx, nullParams)
	if err != nil || consumed != 1 {
		t.Fatalf(
			"consume NULL-session challenge = %d, error = %v",
			consumed,
			err,
		)
	}
	sessionID := fmt.Sprintf("guarded-%d", stamp)
	if _, err := queries.CreateSession(ctx, db.CreateSessionParams{
		ID: sessionID, UserID: user.ID, CsrfToken: "csrf", ExpiresAt: 1_700_003_600,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	boundHash := make([]byte, 32)
	boundHash[0] = 3
	if _, err := queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: boundHash, UserID: user.ID,
		SessionID: sql.NullString{String: sessionID, Valid: true},
		Purpose:   "totp_verify", CsrfHash: csrfHash, CeremonyJson: "{}",
		ExpiresAt: 1_700_000_300,
	}); err != nil {
		t.Fatalf("create bound-session challenge: %v", err)
	}
	boundParams := db.ConsumeMFAChallengeGuardedParams{
		TokenHash: boundHash, UserID: user.ID,
		SessionID: sql.NullString{String: "other", Valid: true},
		Purpose:   "totp_verify", CsrfHash: csrfHash, ExpiresAt: 1_700_000_000,
		Attempts: maxFailures,
	}
	consumed, err = queries.ConsumeMFAChallengeGuarded(ctx, boundParams)
	if err != nil || consumed != 0 {
		t.Fatalf(
			"consume wrong-session challenge = %d, error = %v",
			consumed,
			err,
		)
	}
	boundParams.SessionID = sql.NullString{String: sessionID, Valid: true}
	consumed, err = queries.ConsumeMFAChallengeGuarded(ctx, boundParams)
	if err != nil || consumed != 1 {
		t.Fatalf(
			"consume bound-session challenge = %d, error = %v",
			consumed,
			err,
		)
	}
}

package migrate

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"durpdeploy/internal/db"
)

func TestPostgres_OIDCSchemaParity(t *testing.T) {
	// Given: a disposable PostgreSQL database.
	ctx := context.Background()
	container, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Skipf("PostgreSQL container unavailable: %v", err)
	}
	t.Cleanup(func() { _ = container.Terminate(context.Background()) })
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("PostgreSQL DSN: %v", err)
	}
	conn, err := Run(dsn)
	if err != nil {
		t.Fatalf("migrate PostgreSQL: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	// When: identity and one-time transaction persistence are exercised.
	assertOIDCSchemaParity(t, conn, "public")

	// Then: the expiry index is present for transaction cleanup.
	var count int
	err = conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = 'oidc_transactions'
		AND indexname = 'idx_oidc_transactions_expires_at'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("find PostgreSQL OIDC expiry index: %v", err)
	}
	if count != 1 {
		t.Fatalf("PostgreSQL OIDC expiry index count = %d, want 1", count)
	}
}

func TestMSSQL_OIDCSchemaParity(t *testing.T) {
	// Given: a native SQL Server database.
	ctx := context.Background()
	conn := newSQLServerTestDB(t)

	// When: identity and one-time transaction persistence are exercised.
	assertOIDCSchemaParity(t, conn, "dbo")

	// Then: the expiry index is present for transaction cleanup.
	var count int
	err := conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sys.indexes
		WHERE object_id = OBJECT_ID('dbo.oidc_transactions')
		AND name = 'idx_oidc_transactions_expires_at'`,
	).Scan(&count)
	if err != nil {
		t.Fatalf("find SQL Server OIDC expiry index: %v", err)
	}
	if count != 1 {
		t.Fatalf("SQL Server OIDC expiry index count = %d, want 1", count)
	}
	var keyWidth int
	err = conn.QueryRowContext(ctx, `
		SELECT SUM(CAST(c.max_length AS INT))
		FROM sys.indexes AS i
		JOIN sys.index_columns AS ic
		  ON ic.object_id = i.object_id AND ic.index_id = i.index_id
		JOIN sys.columns AS c
		  ON c.object_id = ic.object_id AND c.column_id = ic.column_id
		WHERE i.object_id = OBJECT_ID('dbo.oidc_identities')
		  AND i.name = 'uq_oidc_identities_issuer_subject'
		  AND ic.is_included_column = 0`).Scan(&keyWidth)
	if err != nil {
		t.Fatalf("find SQL Server OIDC identity key width: %v", err)
	}
	if keyWidth != 1534 || keyWidth > 1700 {
		t.Fatalf(
			"SQL Server OIDC identity key width = %d, want 1534 <= 1700",
			keyWidth,
		)
	}
}

func assertOIDCSchemaParity(t *testing.T, conn *sql.DB, schema string) {
	t.Helper()
	ctx := context.Background()
	queries := db.New(conn)
	firstUser, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "oidc-first@example.com",
		PasswordHash: "hash",
		Name:         "OIDC First",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("create first OIDC user: %v", err)
	}
	secondUser, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "oidc-second@example.com",
		PasswordHash: "hash",
		Name:         "OIDC Second",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("create second OIDC user: %v", err)
	}
	identity := db.CreateOIDCIdentityParams{
		Issuer: "https://issuer.example", Subject: "subject-1", UserID: firstUser.ID,
	}
	if _, err := queries.CreateOIDCIdentity(ctx, identity); err != nil {
		t.Fatalf("create OIDC identity: %v", err)
	}
	_, err = queries.CreateOIDCIdentity(ctx, db.CreateOIDCIdentityParams{
		Issuer: identity.Issuer, Subject: identity.Subject, UserID: secondUser.ID,
	})
	if err == nil {
		t.Fatal("duplicate OIDC issuer and subject succeeded")
	}
	_, err = queries.CreateOIDCIdentity(ctx, db.CreateOIDCIdentityParams{
		Issuer: "https://other-issuer.example", Subject: "subject-2", UserID: firstUser.ID,
	})
	if err == nil {
		t.Fatal("duplicate OIDC user identity succeeded")
	}
	if err := queries.DeleteUser(ctx, firstUser.ID); err != nil {
		t.Fatalf("delete OIDC user: %v", err)
	}
	_, err = queries.GetOIDCIdentity(ctx, db.GetOIDCIdentityParams{
		Issuer: identity.Issuer, Subject: identity.Subject,
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf(
			"get cascaded OIDC identity error = %v, want sql.ErrNoRows",
			err,
		)
	}

	const (
		liveHash    = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		expiredHash = "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789"
		now         = int64(1_700_000_000)
	)
	for _, transaction := range []struct {
		hash      string
		expiresAt int64
	}{
		{hash: liveHash, expiresAt: now + 300},
		{hash: expiredHash, expiresAt: now},
	} {
		err := queries.CreateOIDCTransaction(
			ctx,
			db.CreateOIDCTransactionParams{
				StateHash: transaction.hash, ExpiresAt: transaction.expiresAt,
			},
		)
		if err != nil {
			t.Fatalf("create OIDC transaction: %v", err)
		}
	}
	for _, stateHash := range []string{
		liveHash[:63] + "g",
		strings.ToUpper(liveHash),
	} {
		if err := queries.CreateOIDCTransaction(
			ctx,
			db.CreateOIDCTransactionParams{
				StateHash: stateHash,
				ExpiresAt: now + 300,
			},
		); err == nil {
			t.Fatalf("non-hex OIDC state hash %q succeeded", stateHash)
		}
	}
	type result struct {
		rows int64
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			rows, err := queries.ConsumeOIDCTransaction(
				ctx,
				db.ConsumeOIDCTransactionParams{
					StateHash: liveHash,
					ExpiresAt: now,
				},
			)
			results <- result{rows: rows, err: err}
		}()
	}
	close(start)
	var consumed int64
	for range 2 {
		outcome := <-results
		if outcome.err != nil {
			t.Fatalf("consume OIDC transaction: %v", outcome.err)
		}
		consumed += outcome.rows
	}
	if consumed != 1 {
		t.Fatalf("consumed OIDC rows = %d, want 1", consumed)
	}
	expiredRows, err := queries.ConsumeOIDCTransaction(
		ctx,
		db.ConsumeOIDCTransactionParams{StateHash: expiredHash, ExpiresAt: now},
	)
	if err != nil {
		t.Fatalf("consume expired OIDC transaction: %v", err)
	}
	if expiredRows != 0 {
		t.Fatalf("expired OIDC consumed rows = %d, want 0", expiredRows)
	}
	if err := queries.DeleteExpiredOIDCTransactions(ctx, now); err != nil {
		t.Fatalf("delete expired OIDC transactions: %v", err)
	}
	var remaining int
	err = conn.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM oidc_transactions WHERE state_hash = ?",
		expiredHash,
	).Scan(&remaining)
	if err != nil {
		t.Fatalf("count expired OIDC transactions: %v", err)
	}
	if remaining != 0 {
		t.Fatalf(
			"expired OIDC transactions after cleanup = %d, want 0",
			remaining,
		)
	}
	rows, err := conn.QueryContext(ctx, `
		SELECT column_name FROM information_schema.columns
		WHERE table_schema = ? AND table_name = 'oidc_transactions'
		ORDER BY ordinal_position`, schema)
	if err != nil {
		t.Fatalf("list OIDC transaction columns: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatalf("scan OIDC transaction column: %v", err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate OIDC transaction columns: %v", err)
	}
	if len(columns) != 2 || columns[0] != "state_hash" ||
		columns[1] != "expires_at" {
		t.Fatalf(
			"OIDC transaction columns = %v, want [state_hash expires_at]",
			columns,
		)
	}
}

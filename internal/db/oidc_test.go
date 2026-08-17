package db_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"

	"durpdeploy/internal/db"
)

func TestOIDCSchema_RejectsDuplicateIdentityAndCascadesUser(t *testing.T) {
	// Given: a migrated database and two local users.
	ctx := context.Background()
	queries, conn := newAuthTestDB(t)
	firstUser, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "oidc-first@example.com",
		PasswordHash: "hash",
		Name:         "OIDC First",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	secondUser, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "oidc-second@example.com",
		PasswordHash: "hash",
		Name:         "OIDC Second",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}
	_, err = queries.CreateOIDCIdentity(ctx, db.CreateOIDCIdentityParams{
		Issuer:  "https://issuer.example",
		Subject: "subject-1",
		UserID:  firstUser.ID,
	})
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}

	// When: the identity pair and user binding are each duplicated.
	_, duplicatePairErr := queries.CreateOIDCIdentity(
		ctx,
		db.CreateOIDCIdentityParams{
			Issuer:  "https://issuer.example",
			Subject: "subject-1",
			UserID:  secondUser.ID,
		},
	)
	_, duplicateUserErr := queries.CreateOIDCIdentity(
		ctx,
		db.CreateOIDCIdentityParams{
			Issuer:  "https://other-issuer.example",
			Subject: "subject-2",
			UserID:  firstUser.ID,
		},
	)
	_, err = conn.ExecContext(
		ctx,
		"DELETE FROM users WHERE id = ?",
		firstUser.ID,
	)
	if err != nil {
		t.Fatalf("delete user: %v", err)
	}

	// Then: both unique constraints reject conflicts and user deletion cascades.
	if duplicatePairErr == nil {
		t.Fatal("duplicate issuer and subject succeeded")
	}
	if duplicateUserErr == nil {
		t.Fatal("duplicate user identity succeeded")
	}
	_, err = queries.GetOIDCIdentity(ctx, db.GetOIDCIdentityParams{
		Issuer:  "https://issuer.example",
		Subject: "subject-1",
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("get cascaded identity error = %v, want sql.ErrNoRows", err)
	}
}

func TestOIDCSchema_TransactionExpiryAndConsumeAllowOneWinner(t *testing.T) {
	// Given: a migrated database with one live and one expired state hash.
	ctx := context.Background()
	queries, conn := newAuthTestDB(t)
	conn.SetMaxOpenConns(1)
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
				StateHash: transaction.hash,
				ExpiresAt: transaction.expiresAt,
			},
		)
		if err != nil {
			t.Fatalf("create OIDC transaction: %v", err)
		}
	}
	for _, stateHash := range []string{
		"not-a-hash",
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
			t.Fatalf("malformed OIDC state hash %q succeeded", stateHash)
		}
	}

	// When: two consumers race for the live state and one consumes expired state.
	type result struct {
		rows int64
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var consumers sync.WaitGroup
	for range 2 {
		consumers.Go(func() {
			<-start
			rows, err := queries.ConsumeOIDCTransaction(
				ctx,
				db.ConsumeOIDCTransactionParams{
					StateHash: liveHash,
					ExpiresAt: now,
				},
			)
			results <- result{rows: rows, err: err}
		})
	}
	close(start)
	consumers.Wait()
	close(results)
	expiredRows, err := queries.ConsumeOIDCTransaction(
		ctx,
		db.ConsumeOIDCTransactionParams{
			StateHash: expiredHash,
			ExpiresAt: now,
		},
	)
	if err != nil {
		t.Fatalf("expired OIDC rows affected: %v", err)
	}

	// Then: exactly one live consumer succeeds and expiry rejects consumption.
	var consumed int64
	for outcome := range results {
		if outcome.err != nil {
			t.Fatalf("consume OIDC transaction: %v", outcome.err)
		}
		consumed += outcome.rows
	}
	if consumed != 1 {
		t.Fatalf("consumed OIDC rows = %d, want 1", consumed)
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
	rows, err := conn.QueryContext(ctx, "PRAGMA table_info(oidc_transactions)")
	if err != nil {
		t.Fatalf("inspect OIDC transaction columns: %v", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var (
			columnID     int
			name         string
			columnType   string
			notNull      int
			defaultValue sql.NullString
			primaryKey   int
		)
		if err := rows.Scan(
			&columnID, &name, &columnType, &notNull, &defaultValue, &primaryKey,
		); err != nil {
			t.Fatalf("scan OIDC transaction column: %v", err)
		}
		columns = append(columns, name)
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
	var indexName string
	err = conn.QueryRowContext(ctx, `
		SELECT name FROM pragma_index_list('oidc_transactions')
		WHERE name = 'idx_oidc_transactions_expires_at'`,
	).Scan(&indexName)
	if err != nil {
		t.Fatalf("find OIDC expiry index: %v", err)
	}
}

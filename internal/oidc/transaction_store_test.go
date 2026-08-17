package oidc_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/oidc"
	"durpdeploy/internal/repository"
)

func TestTransactionStore_StartPersistsOnlyStateHash(t *testing.T) {
	// Given
	store, repo := mustTransactionStore(t, func() time.Time {
		return transactionNow
	})

	// When
	transaction, cookie, err := store.Start(
		context.Background(),
		oidc.TransactionRequest{
			Mode: oidc.TransactionModeLogin,
		},
	)

	// Then
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for name, value := range map[string]string{
		"state": transaction.State, "nonce": transaction.Nonce,
		"PKCE verifier": transaction.PKCEVerifier,
	} {
		raw, decodeErr := base64.RawURLEncoding.DecodeString(value)
		if decodeErr != nil || len(raw) != 32 {
			t.Fatalf("%s is not a 256-bit URL-safe value", name)
		}
	}
	wantHash := sha256.Sum256([]byte(transaction.State))
	var gotHash string
	var gotExpiry int64
	if err := repo.DB.QueryRowContext(context.Background(), `
		SELECT state_hash, expires_at FROM oidc_transactions`).Scan(
		&gotHash,
		&gotExpiry,
	); err != nil {
		t.Fatalf("read stored transaction: %v", err)
	}
	if gotHash != hex.EncodeToString(wantHash[:]) ||
		gotExpiry != transaction.ExpiresAt.Unix() {
		t.Fatalf(
			"stored transaction = (%q, %d), want state hash and expiry",
			gotHash,
			gotExpiry,
		)
	}
	request := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	request.AddCookie(cookie)
	decoded, err := mustCodec(t, mustBox(t, 0), false).ReadCookie(request)
	if err != nil || decoded != transaction {
		t.Fatalf("stored cookie transaction = %+v, error = %v", decoded, err)
	}
}

func TestTransactionStore_StartCleansExpiredRows(t *testing.T) {
	// Given
	store, repo := mustTransactionStore(t, func() time.Time {
		return transactionNow
	})
	expiredHash := strings.Repeat("e", 64)
	if err := repo.Queries.CreateOIDCTransaction(
		context.Background(),
		db.CreateOIDCTransactionParams{
			StateHash: expiredHash, ExpiresAt: transactionNow.Add(-time.Second).Unix(),
		},
	); err != nil {
		t.Fatalf("create expired transaction: %v", err)
	}

	// When
	_, _, err := store.Start(context.Background(), oidc.TransactionRequest{
		Mode: oidc.TransactionModeLogin,
	})

	// Then
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	var remaining int
	if err := repo.DB.QueryRowContext(
		context.Background(),
		"SELECT COUNT(*) FROM oidc_transactions WHERE state_hash = ?",
		expiredHash,
	).Scan(&remaining); err != nil {
		t.Fatalf("count expired transaction: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expired transactions = %d, want 0", remaining)
	}
}

func TestTransactionStore_StartRejectsInvalidMode(t *testing.T) {
	// Given
	store, _ := mustTransactionStore(t, func() time.Time {
		return transactionNow
	})

	// When
	_, _, err := store.Start(context.Background(), oidc.TransactionRequest{
		Mode: "invalid",
	})

	// Then
	if !errors.Is(err, oidc.ErrInvalidTransaction) {
		t.Fatalf("Start error = %v, want invalid transaction", err)
	}
}

func mustTransactionStore(
	t *testing.T,
	now func() time.Time,
) (*oidc.TransactionStore, *repository.Repository) {
	t.Helper()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	codec, err := oidc.NewTransactionCookieCodec(
		mustBox(t, 0), oidc.TransactionCookieConfig{Now: now},
	)
	if err != nil {
		t.Fatalf("NewTransactionCookieCodec: %v", err)
	}
	repo := repository.New(conn)
	store, err := oidc.NewTransactionStore(oidc.TransactionStoreOptions{
		Repository: repo, CookieCodec: codec,
	})
	if err != nil {
		t.Fatalf("NewTransactionStore: %v", err)
	}
	return store, repo
}

func mustStartedLogin(
	t *testing.T,
	store *oidc.TransactionStore,
) (oidc.Transaction, *http.Cookie) {
	t.Helper()
	transaction, cookie, err := store.Start(
		context.Background(),
		oidc.TransactionRequest{Mode: oidc.TransactionModeLogin},
	)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return transaction, cookie
}

package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

const transactionRandomBytes = 32

// TransactionStoreOptions configures a short-lived, single-use OIDC store.
type TransactionStoreOptions struct {
	Repository  *repository.Repository
	CookieCodec *TransactionCookieCodec
}

// TransactionStore persists only a hash and expiry for each browser transaction.
type TransactionStore struct {
	repository  *repository.Repository
	cookieCodec *TransactionCookieCodec
	now         func() time.Time
}

// TransactionRequest describes the bindings for a new authorization request.
type TransactionRequest struct {
	Mode   TransactionMode
	Reauth ReauthBinding
}

// CallbackRequest contains callback data needed before an OIDC code exchange.
// HasProviderError is true when the provider returned an error callback parameter.
type CallbackRequest struct {
	Request          *http.Request
	Response         http.ResponseWriter
	State            string
	HasProviderError bool
}

func NewTransactionStore(
	options TransactionStoreOptions,
) (*TransactionStore, error) {
	if options.Repository == nil || options.CookieCodec == nil ||
		options.CookieCodec.Now == nil {
		return nil, ErrInvalidTransaction
	}
	return &TransactionStore{
		repository: options.Repository, cookieCodec: options.CookieCodec,
		now: options.CookieCodec.Now,
	}, nil
}

// Start creates a transaction and persists its state hash before returning the
// encrypted browser cookie. A later start replaces the same browser cookie.
func (s *TransactionStore) Start(
	ctx context.Context,
	request TransactionRequest,
) (Transaction, *http.Cookie, error) {
	now := s.now()
	transaction, err := newTransaction(request, now)
	if err != nil {
		return Transaction{}, nil, ErrInvalidTransaction
	}
	cookie, err := s.cookieCodec.NewCookie(transaction)
	if err != nil {
		return Transaction{}, nil, ErrInvalidTransaction
	}
	if err := s.repository.Queries.DeleteExpiredOIDCTransactions(
		ctx,
		now.Unix(),
	); err != nil {
		return Transaction{}, nil, fmt.Errorf(
			"clean expired OIDC transactions: %w",
			err,
		)
	}
	if err := s.repository.Queries.CreateOIDCTransaction(
		ctx,
		db.CreateOIDCTransactionParams{
			StateHash: stateHash(transaction.State),
			ExpiresAt: transaction.ExpiresAt.Unix(),
		},
	); err != nil {
		return Transaction{}, nil, fmt.Errorf(
			"persist OIDC transaction: %w",
			err,
		)
	}
	return transaction, cookie, nil
}

// Consume clears the browser cookie and atomically consumes a valid transaction
// before returning it for token exchange. Invalid callback data is deliberately
// indistinguishable to callers.
func (s *TransactionStore) Consume(
	ctx context.Context,
	callback CallbackRequest,
) (Transaction, error) {
	if callback.Request == nil || callback.Response == nil {
		return Transaction{}, ErrInvalidTransaction
	}
	http.SetCookie(callback.Response, s.cookieCodec.ClearCookie())
	now := s.now()
	if err := s.repository.Queries.DeleteExpiredOIDCTransactions(
		ctx,
		now.Unix(),
	); err != nil {
		return Transaction{}, fmt.Errorf(
			"clean expired OIDC transactions: %w",
			err,
		)
	}
	transaction, err := s.cookieCodec.ReadCookie(callback.Request)
	if err != nil || !transaction.MatchesState(callback.State) {
		return Transaction{}, ErrInvalidTransaction
	}
	consumed, err := s.repository.Queries.ConsumeOIDCTransaction(
		ctx,
		db.ConsumeOIDCTransactionParams{
			StateHash: stateHash(transaction.State), ExpiresAt: now.Unix(),
		},
	)
	if err != nil {
		return Transaction{}, fmt.Errorf("consume OIDC transaction: %w", err)
	}
	if consumed != 1 || callback.HasProviderError {
		return Transaction{}, ErrInvalidTransaction
	}
	return transaction, nil
}

func newTransaction(
	request TransactionRequest,
	now time.Time,
) (Transaction, error) {
	state, err := randomTransactionValue()
	if err != nil {
		return Transaction{}, err
	}
	nonce, err := randomTransactionValue()
	if err != nil {
		return Transaction{}, err
	}
	verifier, err := randomTransactionValue()
	if err != nil {
		return Transaction{}, err
	}
	return Transaction{
		Mode: request.Mode, State: state, Nonce: nonce, PKCEVerifier: verifier,
		ExpiresAt: now.Add(transactionLifetime), Reauth: request.Reauth,
	}, nil
}

func randomTransactionValue() (string, error) {
	raw := make([]byte, transactionRandomBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func stateHash(state string) string {
	hash := sha256.Sum256([]byte(state))
	return hex.EncodeToString(hash[:])
}

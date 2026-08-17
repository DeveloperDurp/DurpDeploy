package oidc_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/oidc"
)

func TestTransactionStore_ConsumeHasOneConcurrentWinner(t *testing.T) {
	// Given
	store, _ := mustTransactionStore(t, func() time.Time {
		return transactionNow
	})
	transaction, cookie := mustStartedLogin(t, store)
	type result struct {
		err     error
		cleared bool
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	var consumers sync.WaitGroup
	for range 2 {
		consumers.Go(func() {
			request := httptest.NewRequest("GET", "/login/oidc/callback", nil)
			request.AddCookie(cookie)
			response := httptest.NewRecorder()
			<-start
			_, err := store.Consume(context.Background(), oidc.CallbackRequest{
				Request: request, Response: response, State: transaction.State,
			})
			results <- result{err: err, cleared: transactionCookieWasCleared(response)}
		})
	}

	// When
	close(start)
	consumers.Wait()
	close(results)

	// Then
	var winners int
	for result := range results {
		if result.err == nil {
			winners++
		} else if !errors.Is(result.err, oidc.ErrInvalidTransaction) {
			t.Fatalf("Consume error = %v, want invalid transaction", result.err)
		}
		if !result.cleared {
			t.Fatal("callback did not clear the transaction cookie")
		}
	}
	if winners != 1 {
		t.Fatalf("concurrent winners = %d, want 1", winners)
	}
}

func TestTransactionStore_ConsumeRejectsReplay(t *testing.T) {
	// Given
	store, _ := mustTransactionStore(t, func() time.Time {
		return transactionNow
	})
	transaction, cookie := mustStartedLogin(t, store)
	firstResponse := httptest.NewRecorder()
	firstRequest := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	firstRequest.AddCookie(cookie)
	if _, err := store.Consume(context.Background(), oidc.CallbackRequest{
		Request: firstRequest, Response: firstResponse, State: transaction.State,
	}); err != nil {
		t.Fatalf("first Consume: %v", err)
	}
	replayResponse := httptest.NewRecorder()
	replayRequest := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	replayRequest.AddCookie(cookie)

	// When
	_, err := store.Consume(context.Background(), oidc.CallbackRequest{
		Request: replayRequest, Response: replayResponse, State: transaction.State,
	})

	// Then
	if !errors.Is(err, oidc.ErrInvalidTransaction) {
		t.Fatalf("replay error = %v, want invalid transaction", err)
	}
	if !transactionCookieWasCleared(firstResponse) ||
		!transactionCookieWasCleared(replayResponse) {
		t.Fatal("every callback outcome must clear the transaction cookie")
	}
}

func TestTransactionStore_ConsumeRejectsMismatchMissingAndExpiredCallbacks(
	t *testing.T,
) {
	tests := []struct {
		name    string
		request func(*http.Cookie) *http.Request
		state   func(oidc.Transaction) string
		expire  bool
	}{
		{
			name: "mismatched state",
			request: func(cookie *http.Cookie) *http.Request {
				request := httptest.NewRequest(
					"GET",
					"/login/oidc/callback",
					nil,
				)
				request.AddCookie(cookie)
				return request
			},
			state: func(transaction oidc.Transaction) string {
				return transaction.State + "mismatch"
			},
		},
		{
			name: "missing cookie",
			request: func(*http.Cookie) *http.Request {
				return httptest.NewRequest("GET", "/login/oidc/callback", nil)
			},
			state: func(transaction oidc.Transaction) string { return transaction.State },
		},
		{
			name: "expired cookie",
			request: func(cookie *http.Cookie) *http.Request {
				request := httptest.NewRequest(
					"GET",
					"/login/oidc/callback",
					nil,
				)
				request.AddCookie(cookie)
				return request
			},
			state:  func(transaction oidc.Transaction) string { return transaction.State },
			expire: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			now := transactionNow
			store, _ := mustTransactionStore(t, func() time.Time { return now })
			transaction, cookie := mustStartedLogin(t, store)
			if test.expire {
				now = transaction.ExpiresAt
			}
			response := httptest.NewRecorder()

			// When
			_, err := store.Consume(context.Background(), oidc.CallbackRequest{
				Request: test.request(cookie), Response: response,
				State: test.state(transaction),
			})

			// Then
			if !errors.Is(err, oidc.ErrInvalidTransaction) {
				t.Fatalf("Consume error = %v, want invalid transaction", err)
			}
			if !transactionCookieWasCleared(response) {
				t.Fatal("invalid callback did not clear the transaction cookie")
			}
		})
	}
}

func TestTransactionStore_ConsumeConsumesProviderError(t *testing.T) {
	// Given
	store, _ := mustTransactionStore(t, func() time.Time {
		return transactionNow
	})
	transaction, cookie := mustStartedLogin(t, store)
	providerErrorResponse := httptest.NewRecorder()
	providerErrorRequest := httptest.NewRequest(
		"GET",
		"/login/oidc/callback",
		nil,
	)
	providerErrorRequest.AddCookie(cookie)

	// When
	_, err := store.Consume(context.Background(), oidc.CallbackRequest{
		Request: providerErrorRequest, Response: providerErrorResponse,
		State: transaction.State, HasProviderError: true,
	})

	// Then
	if !errors.Is(err, oidc.ErrInvalidTransaction) {
		t.Fatalf("provider callback error = %v, want invalid transaction", err)
	}
	if !transactionCookieWasCleared(providerErrorResponse) {
		t.Fatal("provider-error callback did not clear the transaction cookie")
	}
	replayResponse := httptest.NewRecorder()
	replayRequest := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	replayRequest.AddCookie(cookie)
	if _, err := store.Consume(context.Background(), oidc.CallbackRequest{
		Request: replayRequest, Response: replayResponse, State: transaction.State,
	}); !errors.Is(err, oidc.ErrInvalidTransaction) {
		t.Fatalf("provider-error replay = %v, want invalid transaction", err)
	}
}

func transactionCookieWasCleared(response *httptest.ResponseRecorder) bool {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == oidc.TransactionCookieName && cookie.MaxAge < 0 {
			return true
		}
	}
	return false
}

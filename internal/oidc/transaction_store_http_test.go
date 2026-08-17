package oidc_test

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/oidc"
)

func TestTransactionStore_LatestTabWinsAndOldCallbackFails(t *testing.T) {
	// Given
	store, _ := mustTransactionStore(t, func() time.Time {
		return transactionNow
	})
	var first, latest oidc.Transaction
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		switch request.URL.Path {
		case "/login/oidc/start/first", "/login/oidc/start/latest":
			transaction, cookie, err := store.Start(
				request.Context(),
				oidc.TransactionRequest{Mode: oidc.TransactionModeLogin},
			)
			if err != nil {
				http.Error(
					response,
					"start failed",
					http.StatusInternalServerError,
				)
				return
			}
			if request.URL.Path == "/login/oidc/start/first" {
				first = transaction
			} else {
				latest = transaction
			}
			http.SetCookie(response, cookie)
		case "/login/oidc/callback/first", "/login/oidc/callback/latest":
			transaction := first
			if request.URL.Path == "/login/oidc/callback/latest" {
				transaction = latest
			}
			_, err := store.Consume(request.Context(), oidc.CallbackRequest{
				Request: request, Response: response, State: transaction.State,
			})
			if err != nil {
				http.Error(response, "invalid callback", http.StatusBadRequest)
			}
		default:
			http.NotFound(response, request)
		}
	}))
	t.Cleanup(server.Close)

	// When
	latestClient := clientWithCookieJar(t, server)
	for _, path := range []string{
		"/login/oidc/start/first",
		"/login/oidc/start/latest",
		"/login/oidc/callback/latest",
	} {
		if status := getStatus(
			t,
			latestClient,
			server.URL+path,
		); status != http.StatusOK {
			t.Fatalf(
				"latest callback status = %d, want %d",
				status,
				http.StatusOK,
			)
		}
	}
	oldClient := clientWithCookieJar(t, server)
	for _, path := range []string{
		"/login/oidc/start/first",
		"/login/oidc/start/latest",
	} {
		if status := getStatus(
			t,
			oldClient,
			server.URL+path,
		); status != http.StatusOK {
			t.Fatalf("start status = %d, want %d", status, http.StatusOK)
		}
	}
	oldCallbackStatus := getStatus(
		t,
		oldClient,
		server.URL+"/login/oidc/callback/first",
	)

	// Then
	if oldCallbackStatus != http.StatusBadRequest {
		t.Fatalf(
			"old-tab callback status = %d, want %d",
			oldCallbackStatus,
			http.StatusBadRequest,
		)
	}
}

func clientWithCookieJar(
	t *testing.T,
	server *httptest.Server,
) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("New cookie jar: %v", err)
	}
	client := server.Client()
	client.Jar = jar
	return client
}

func getStatus(t *testing.T, client *http.Client, rawURL string) int {
	t.Helper()
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		rawURL,
		nil,
	)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET %s: %v", rawURL, err)
	}
	if err := response.Body.Close(); err != nil {
		t.Fatalf("close response body: %v", err)
	}
	return response.StatusCode
}

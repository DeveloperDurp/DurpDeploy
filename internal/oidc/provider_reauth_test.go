package oidc

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestProviderReauthRefreshesExistingAuthentikSession(t *testing.T) {
	fixture := newProviderFixture(t)
	fixture.config.GroupClaim = "groups"
	fixture.config.AdminGroup = "fixture-admin"
	fixture.EmulateAuthentikReauth()
	provider := fixture.newProvider(t)
	transaction := fixture.transaction
	transaction.Mode = TransactionModeReauth
	transaction.ExpiresAt = fixture.now.Add(transactionLifetime)
	location, err := provider.AuthorizationURL(
		context.Background(),
		transaction,
	)
	if err != nil {
		t.Fatal(err)
	}
	client := *fixture.client
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Get(location)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusFound {
		t.Fatalf("authorize = %d", response.StatusCode)
	}
	callback, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	claims, err := provider.Exchange(
		context.Background(), callback.Query().Get("code"), transaction,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.ParseReauthenticationClaims(claims, transaction); err != nil {
		t.Fatalf("existing Authentik session was not reauthenticated: %v", err)
	}
	if fixture.Counters().Reauthenticate != 1 {
		t.Fatal("provider reused its existing sign-in")
	}
}

func TestProviderReauthStillRejectsAuthenticationBeforeTransaction(
	t *testing.T,
) {
	fixture := newProviderFixture(t)
	fixture.config.GroupClaim = "groups"
	fixture.config.AdminGroup = "fixture-admin"
	provider := fixture.newProvider(t)
	transaction := fixture.transaction
	transaction.Mode = TransactionModeReauth
	transaction.ExpiresAt = fixture.now.Add(transactionLifetime)
	for _, offset := range []time.Duration{-time.Second, 0, time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			claims := []byte(fmt.Sprintf(
				`{"sub":"fixture-subject","email":"fixture@example.test","email_verified":true,"groups":["fixture-admin"],"auth_time":%d}`,
				fixture.now.Add(offset).Unix(),
			))
			_, err := provider.ParseReauthenticationClaims(claims, transaction)
			if (err == nil) != (offset == 0) {
				t.Fatalf("auth_time offset %s: error = %v", offset, err)
			}
		})
	}
}

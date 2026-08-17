package oidctest_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"

	"golang.org/x/oauth2"
)

func TestFixtureProviderCompletesTLSFlowAndCapturesPKCE(t *testing.T) {
	// Given
	fixture := newFixture(t)
	fixture.SetClaims(oidctest.Claims{
		Subject:       "browser-subject",
		Email:         "browser@example.test",
		EmailVerified: true,
		Groups:        []string{"fixture-admin"},
	})
	provider := newProvider(t, fixture)
	transaction := testTransaction()

	// When
	authorizationURL, err := provider.AuthorizationURL(
		context.Background(),
		transaction,
	)
	if err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}
	client := *fixture.Client()
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Get(authorizationURL)
	if err != nil {
		t.Fatalf("GET authorization URL: %v", err)
	}
	defer response.Body.Close()
	callback, err := url.Parse(response.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse authorization callback: %v", err)
	}
	claims, err := provider.Exchange(
		context.Background(),
		callback.Query().Get("code"),
		transaction,
	)

	// Then
	if response.StatusCode != http.StatusFound {
		t.Fatalf(
			"authorize status = %d, want %d",
			response.StatusCode,
			http.StatusFound,
		)
	}
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if callback.Query().Get("state") != transaction.State {
		t.Fatal("authorization callback did not preserve state")
	}
	capture := fixture.Capture()
	if capture.Authorization.CodeChallengeMethod != "S256" ||
		capture.Authorization.CodeChallenge != oauth2.S256ChallengeFromVerifier(
			transaction.PKCEVerifier,
		) {
		t.Fatal("authorize request did not capture the S256 PKCE challenge")
	}
	if capture.Token.CodeVerifier != transaction.PKCEVerifier {
		t.Fatal("token request did not capture the PKCE verifier")
	}
	if got, want := fixture.Counters(), (oidctest.Counters{
		Discovery: 1, Authorize: 1, Token: 1, JWKS: 1,
	}); got != want {
		t.Fatalf("counters = %#v, want %#v", got, want)
	}
	var identity struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
	}
	if err := json.Unmarshal(claims, &identity); err != nil {
		t.Fatalf("unmarshal claims: %v", err)
	}
	if identity.Subject != "browser-subject" ||
		identity.Email != "browser@example.test" {
		t.Fatalf("claims = %#v, want configured claims", identity)
	}
}

func TestFixtureAuthorizeRejectsInvalidRequest(t *testing.T) {
	tests := []struct {
		name         string
		redirectURL  string
		responseType string
	}{
		{
			name:         "unregistered redirect",
			redirectURL:  "https://unregistered.example/callback",
			responseType: "code",
		},
		{
			name:         "unexpected response type",
			redirectURL:  "https://deploy.example/login/oidc/callback",
			responseType: "token",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			fixture := newFixture(t)
			values := url.Values{
				"client_id":             {fixture.ClientID()},
				"redirect_uri":          {test.redirectURL},
				"response_type":         {test.responseType},
				"scope":                 {"openid"},
				"state":                 {"fixture-state-123456"},
				"nonce":                 {"fixture-nonce-123456"},
				"code_challenge":        {"fixture-challenge"},
				"code_challenge_method": {"S256"},
			}
			client := *fixture.Client()
			client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			}

			// When
			response, err := client.Get(
				fixture.URL() + "/authorize?" + values.Encode(),
			)

			// Then
			if err != nil {
				t.Fatalf("GET authorize: %v", err)
			}
			defer response.Body.Close()
			if response.StatusCode != http.StatusBadRequest {
				t.Fatalf(
					"authorize status = %d, want %d",
					response.StatusCode,
					http.StatusBadRequest,
				)
			}
		})
	}
}

func TestFixtureProviderRejectsReusedAuthorizationCode(t *testing.T) {
	// Given
	fixture := newFixture(t)
	provider := newProvider(t, fixture)
	transaction := testTransaction()
	code := fixture.Code()
	if _, err := provider.Exchange(
		context.Background(),
		code,
		transaction,
	); err != nil {
		t.Fatalf("first Exchange() error = %v", err)
	}

	// When
	_, err := provider.Exchange(context.Background(), code, transaction)

	// Then
	assertProviderError(t, err, oidc.ProviderErrorExchange)
	if got := fixture.Counters().Token; got < 2 {
		t.Fatalf("token requests = %d, want at least 2", got)
	}
}

func TestFixtureProviderRefreshesJWKSAfterRotation(t *testing.T) {
	// Given
	fixture := newFixture(t)
	provider := newProvider(t, fixture)
	transaction := testTransaction()
	if _, err := provider.Exchange(
		context.Background(),
		fixture.Code(),
		transaction,
	); err != nil {
		t.Fatalf("first Exchange() error = %v", err)
	}
	if err := fixture.RotateSigningKey(); err != nil {
		t.Fatalf("RotateSigningKey() error = %v", err)
	}

	// When
	_, err := provider.Exchange(
		context.Background(),
		fixture.NewCode(),
		transaction,
	)

	// Then
	if err != nil {
		t.Fatalf("rotated Exchange() error = %v", err)
	}
	if got, want := fixture.Counters().JWKS, int32(2); got != want {
		t.Fatalf("JWKS requests = %d, want %d", got, want)
	}
}

func newFixture(t *testing.T) *oidctest.Fixture {
	t.Helper()
	fixture, err := oidctest.New(oidctest.Options{
		Now: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("oidctest.New() error = %v", err)
	}
	t.Cleanup(fixture.Close)
	return fixture
}

func newProvider(t *testing.T, fixture *oidctest.Fixture) *oidc.Provider {
	t.Helper()
	provider, err := oidc.NewProvider(oidc.ProviderOptions{
		Config: oidc.Config{
			Enabled:      true,
			Issuer:       fixture.URL(),
			ClientID:     fixture.ClientID(),
			ClientSecret: fixture.ClientSecret(),
			CallbackURL:  "https://deploy.example/login/oidc/callback",
			Scopes:       []string{"openid", "profile", "email"},
		},
		HTTPClient: fixture.Client(),
		Now:        fixture.Now,
	})
	if err != nil {
		t.Fatalf("oidc.NewProvider() error = %v", err)
	}
	return provider
}

func testTransaction() oidc.Transaction {
	return oidc.Transaction{
		State:        "fixture-state-123456",
		Nonce:        "fixture-nonce-123456",
		PKCEVerifier: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
}

func exchangeFixtureCode(
	provider *oidc.Provider,
	fixture *oidctest.Fixture,
) error {
	_, err := provider.Exchange(
		context.Background(),
		fixture.Code(),
		testTransaction(),
	)
	return err
}

func assertProviderError(
	t *testing.T,
	err error,
	want oidc.ProviderErrorKind,
) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want provider error")
	}
	var providerErr *oidc.ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error type = %T, want *oidc.ProviderError", err)
	}
	if providerErr.Kind != want {
		t.Fatalf("provider error kind = %q, want %q", providerErr.Kind, want)
	}
}

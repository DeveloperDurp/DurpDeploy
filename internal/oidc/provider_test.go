package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/oidc/oidctest"

	"golang.org/x/oauth2"
)

func TestProviderAuthorizationURLDiscoversLazilyAndUsesS256PKCE(t *testing.T) {
	// Given
	fixture := newProviderFixture(t)
	provider := fixture.newProvider(t)
	if got := fixture.Counters().Discovery; got != 0 {
		t.Fatalf("discovery requests after construction = %d, want 0", got)
	}

	// When
	authorizationURL, err := provider.AuthorizationURL(
		context.Background(),
		fixture.transaction,
	)

	// Then
	if err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}
	if got := fixture.Counters().Discovery; got != 1 {
		t.Fatalf("discovery requests = %d, want 1", got)
	}
	assertAuthorizationURL(t, authorizationURL, fixture)
}

func TestProviderAuthorizationURLRetriesFailedDiscoveryAndCachesSuccess(
	t *testing.T,
) {
	// Given
	fixture := newProviderFixture(t)
	fixture.SetDiscoveryFailures(1)
	provider := fixture.newProvider(t)

	// When
	_, firstErr := provider.AuthorizationURL(
		context.Background(),
		fixture.transaction,
	)
	secondURL, secondErr := provider.AuthorizationURL(
		context.Background(),
		fixture.transaction,
	)
	_, thirdErr := provider.AuthorizationURL(
		context.Background(),
		fixture.transaction,
	)

	// Then
	assertProviderError(t, firstErr, ProviderErrorDiscovery)
	if secondErr != nil {
		t.Fatalf("second AuthorizationURL() error = %v", secondErr)
	}
	if thirdErr != nil {
		t.Fatalf("third AuthorizationURL() error = %v", thirdErr)
	}
	if secondURL == "" {
		t.Fatal("second AuthorizationURL() = empty URL")
	}
	if got := fixture.Counters().Discovery; got != 2 {
		t.Fatalf("discovery requests = %d, want 2", got)
	}
}

func TestProviderExchangeUsesVerifierAndReturnsVerifiedClaims(t *testing.T) {
	// Given
	fixture := newProviderFixture(t)
	provider := fixture.newProvider(t)
	if _, err := provider.AuthorizationURL(
		context.Background(),
		fixture.transaction,
	); err != nil {
		t.Fatalf("AuthorizationURL() error = %v", err)
	}

	// When
	claims, err := provider.Exchange(
		context.Background(),
		fixture.authorizationCode,
		fixture.transaction,
	)

	// Then
	if err != nil {
		t.Fatalf("Exchange() error = %v", err)
	}
	if got := fixture.Counters().Token; got != 1 {
		t.Fatalf("token requests = %d, want 1", got)
	}
	if got := fixture.Counters().Discovery; got != 1 {
		t.Fatalf("discovery requests = %d, want cached success", got)
	}
	if got := fixture.Counters().JWKS; got != 1 {
		t.Fatalf("JWKS requests = %d, want 1", got)
	}

	var identity struct {
		Subject string `json:"sub"`
	}
	if err := json.Unmarshal(claims, &identity); err != nil {
		t.Fatalf("unmarshal verified claims: %v", err)
	}
	if identity.Subject != fixture.subject {
		t.Fatalf(
			"verified subject = %q, want configured fixture subject",
			identity.Subject,
		)
	}
}

func TestProviderExchangeRejectsUnverifiedTokens(t *testing.T) {
	tests := []struct {
		name            string
		mode            oidctest.TokenMode
		failTokenServer bool
		want            ProviderErrorKind
	}{
		{
			"token endpoint failure",
			oidctest.TokenValid,
			true,
			ProviderErrorExchange,
		},
		{"missing id token", oidctest.TokenMissing, false, ProviderErrorToken},
		{
			"non-string id token",
			oidctest.TokenNonString,
			false,
			ProviderErrorToken,
		},
		{
			"invalid signature",
			oidctest.TokenBadSignature,
			false,
			ProviderErrorVerification,
		},
		{
			"wrong issuer",
			oidctest.TokenWrongIssuer,
			false,
			ProviderErrorVerification,
		},
		{
			"wrong audience",
			oidctest.TokenWrongAudience,
			false,
			ProviderErrorVerification,
		},
		{"expired", oidctest.TokenExpired, false, ProviderErrorVerification},
		{"wrong nonce", oidctest.TokenWrongNonce, false, ProviderErrorNonce},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Given
			fixture := newProviderFixture(t)
			fixture.SetTokenMode(tt.mode)
			if tt.failTokenServer {
				fixture.SetTokenFailures(1)
			}
			provider := fixture.newProvider(t)

			// When
			_, err := provider.Exchange(
				context.Background(),
				fixture.authorizationCode,
				fixture.transaction,
			)

			// Then
			assertProviderError(t, err, tt.want)
		})
	}
}

func TestProviderAuthorizationURLStopsAtHTTPClientTimeout(t *testing.T) {
	// Given
	fixture := newProviderFixture(t)
	fixture.client.Timeout = 25 * time.Millisecond
	discoveryCanceled := fixture.BlockDiscovery()
	provider := fixture.newProvider(t)

	// When
	_, err := provider.AuthorizationURL(
		context.Background(),
		fixture.transaction,
	)

	// Then
	assertProviderError(t, err, ProviderErrorDiscovery)
	select {
	case <-discoveryCanceled:
	case <-time.After(time.Second):
		t.Fatal("slow TLS discovery request was not canceled")
	}
}

func assertAuthorizationURL(
	t *testing.T,
	rawURL string,
	fixture *providerFixture,
) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse authorization URL: %v", err)
	}
	if parsed.Scheme != "https" ||
		parsed.Host != strings.TrimPrefix(fixture.URL(), "https://") ||
		parsed.Path != "/authorize" {
		t.Fatal(
			"authorization URL does not use the discovered authorization endpoint",
		)
	}
	query := parsed.Query()
	if query.Get("client_id") != fixture.config.ClientID ||
		query.Get("redirect_uri") != fixture.config.CallbackURL ||
		query.Get("response_type") != "code" ||
		query.Get("scope") != strings.Join(fixture.config.Scopes, " ") ||
		query.Get("state") != fixture.transaction.State ||
		query.Get("nonce") != fixture.transaction.Nonce {
		t.Fatal(
			"authorization URL does not preserve fixed client configuration",
		)
	}
	if query.Get("code_challenge_method") != "S256" ||
		query.Get(
			"code_challenge",
		) != oauth2.S256ChallengeFromVerifier(
			fixture.transaction.PKCEVerifier,
		) {
		t.Fatal("authorization URL does not use S256 PKCE")
	}
}

func assertProviderError(t *testing.T, err error, want ProviderErrorKind) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want provider error")
	}
	var providerErr *ProviderError
	if !errors.As(err, &providerErr) {
		t.Fatalf("error type = %T, want *ProviderError", err)
	}
	if providerErr.Kind != want {
		t.Fatalf("provider error kind = %q, want %q", providerErr.Kind, want)
	}
	if strings.Contains(err.Error(), providerFixtureDiagnostic) {
		t.Fatal(
			"provider error exposes upstream diagnostics or sensitive material",
		)
	}
}

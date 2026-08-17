package oidctest_test

import (
	"context"
	"testing"

	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
)

func TestFixtureProviderReturnsEachTodoFiveFailure(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*oidctest.Fixture)
		call      func(*oidc.Provider, *oidctest.Fixture) error
		want      oidc.ProviderErrorKind
	}{
		{
			name: "discovery failure",
			configure: func(fixture *oidctest.Fixture) {
				fixture.SetDiscoveryFailures(1)
			},
			call: func(provider *oidc.Provider, _ *oidctest.Fixture) error {
				_, err := provider.AuthorizationURL(
					context.Background(),
					testTransaction(),
				)
				return err
			},
			want: oidc.ProviderErrorDiscovery,
		},
		{
			name: "token endpoint failure",
			configure: func(fixture *oidctest.Fixture) {
				fixture.SetTokenFailures(1)
			},
			call: exchangeFixtureCode,
			want: oidc.ProviderErrorExchange,
		},
		{
			name: "missing ID token",
			configure: func(fixture *oidctest.Fixture) {
				fixture.SetTokenMode(oidctest.TokenMissing)
			},
			call: exchangeFixtureCode,
			want: oidc.ProviderErrorToken,
		},
		{
			name: "non-string ID token",
			configure: func(fixture *oidctest.Fixture) {
				fixture.SetTokenMode(oidctest.TokenNonString)
			},
			call: exchangeFixtureCode,
			want: oidc.ProviderErrorToken,
		},
		{
			name: "invalid verifier signature",
			configure: func(fixture *oidctest.Fixture) {
				fixture.SetTokenMode(oidctest.TokenBadSignature)
			},
			call: exchangeFixtureCode,
			want: oidc.ProviderErrorVerification,
		},
		{
			name: "wrong issuer",
			configure: func(fixture *oidctest.Fixture) {
				fixture.SetTokenMode(oidctest.TokenWrongIssuer)
			},
			call: exchangeFixtureCode,
			want: oidc.ProviderErrorVerification,
		},
		{
			name: "wrong audience",
			configure: func(fixture *oidctest.Fixture) {
				fixture.SetTokenMode(oidctest.TokenWrongAudience)
			},
			call: exchangeFixtureCode,
			want: oidc.ProviderErrorVerification,
		},
		{
			name: "expired token",
			configure: func(fixture *oidctest.Fixture) {
				fixture.SetTokenMode(oidctest.TokenExpired)
			},
			call: exchangeFixtureCode,
			want: oidc.ProviderErrorVerification,
		},
		{
			name: "wrong nonce",
			configure: func(fixture *oidctest.Fixture) {
				fixture.SetTokenMode(oidctest.TokenWrongNonce)
			},
			call: exchangeFixtureCode,
			want: oidc.ProviderErrorNonce,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			fixture := newFixture(t)
			test.configure(fixture)
			provider := newProvider(t, fixture)

			// When
			err := test.call(provider, fixture)

			// Then
			assertProviderError(t, err, test.want)
		})
	}
}

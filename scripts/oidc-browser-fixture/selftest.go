//go:build oidctest

package main

import (
	"context"
	"fmt"

	"durpdeploy/internal/oidc"
	"durpdeploy/internal/oidc/oidctest"
)

func runSelfTest(ctx context.Context) error {
	fixture, err := oidctest.New(oidctest.Options{})
	if err != nil {
		return fmt.Errorf("start TLS OIDC fixture: %w", err)
	}
	defer fixture.Close()
	return verifyTrustedOIDC(ctx, fixture)
}

func verifyTrustedOIDC(ctx context.Context, fixture *oidctest.Fixture) error {
	provider, err := oidc.NewProvider(oidc.ProviderOptions{
		Config: oidc.Config{
			Enabled:      true,
			Issuer:       fixture.URL(),
			ClientID:     fixture.ClientID(),
			ClientSecret: fixture.ClientSecret(),
			CallbackURL:  "https://127.0.0.1/login/oidc/callback",
			Scopes:       []string{"openid", "profile", "email"},
		},
		HTTPClient: fixture.Client(),
		Now:        fixture.Now,
	})
	if err != nil {
		return fmt.Errorf(
			"construct OIDC provider with trusted fixture client: %w",
			err,
		)
	}
	transaction := oidc.Transaction{
		State:        "fixture-state-123456",
		Nonce:        "fixture-nonce-123456",
		PKCEVerifier: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
	}
	if _, err := provider.AuthorizationURL(ctx, transaction); err != nil {
		return fmt.Errorf("discover fixture provider: %w", err)
	}
	if _, err := provider.Exchange(
		ctx,
		fixture.Code(),
		transaction,
	); err != nil {
		return fmt.Errorf("exchange fixture authorization code: %w", err)
	}
	return nil
}

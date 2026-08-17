package oidc

import (
	"net/http"
	"testing"
	"time"

	"durpdeploy/internal/oidc/oidctest"
)

const providerFixtureDiagnostic = "fixture token failure"

type providerFixture struct {
	*oidctest.Fixture
	client            *http.Client
	config            Config
	transaction       Transaction
	now               time.Time
	subject           string
	authorizationCode string
}

func newProviderFixture(t *testing.T) *providerFixture {
	t.Helper()
	fixture, err := oidctest.New(oidctest.Options{
		Now: time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("start OIDC test fixture: %v", err)
	}
	t.Cleanup(fixture.Close)
	return &providerFixture{
		Fixture: fixture,
		client:  fixture.Client(),
		config: Config{
			Enabled:      true,
			Issuer:       fixture.URL(),
			ClientID:     fixture.ClientID(),
			ClientSecret: fixture.ClientSecret(),
			CallbackURL:  "https://deploy.example/login/oidc/callback",
			Scopes:       []string{"openid", "profile", "email"},
		},
		transaction: Transaction{
			State:        "fixture-state-123456",
			Nonce:        "fixture-nonce-123456",
			PKCEVerifier: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		},
		now:               fixture.Now(),
		subject:           "fixture-subject",
		authorizationCode: fixture.Code(),
	}
}

func (f *providerFixture) newProvider(t *testing.T) *Provider {
	t.Helper()
	provider, err := NewProvider(ProviderOptions{
		Config:     f.config,
		HTTPClient: f.client,
		Now: func() time.Time {
			return f.now
		},
	})
	if err != nil {
		t.Fatalf("NewProvider() error = %v", err)
	}
	return provider
}

func (f *providerFixture) rotateSigningKey(t *testing.T) {
	t.Helper()
	if err := f.RotateSigningKey(); err != nil {
		t.Fatalf("rotate fixture signing key: %v", err)
	}
}

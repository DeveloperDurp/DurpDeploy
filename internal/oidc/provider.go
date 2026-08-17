package oidc

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type ProviderErrorKind string

const (
	ProviderErrorConfiguration ProviderErrorKind = "configuration"
	ProviderErrorDiscovery     ProviderErrorKind = "discovery"
	ProviderErrorExchange      ProviderErrorKind = "exchange"
	ProviderErrorToken         ProviderErrorKind = "token"
	ProviderErrorVerification  ProviderErrorKind = "verification"
	ProviderErrorNonce         ProviderErrorKind = "nonce"
)

// ProviderError identifies an SSO failure without retaining provider data.
type ProviderError struct {
	Kind ProviderErrorKind
}

func (e *ProviderError) Error() string {
	switch e.Kind {
	case ProviderErrorVerification, ProviderErrorToken, ProviderErrorNonce:
		return "single sign-on could not be verified"
	default:
		return "single sign-on is temporarily unavailable"
	}
}

type ProviderOptions struct {
	Config     Config
	HTTPClient *http.Client
	Now        func() time.Time
}

// Provider performs lazy OIDC discovery and verifies callback ID tokens.
type Provider struct {
	config     Config
	httpClient *http.Client
	now        func() time.Time

	mu         sync.Mutex
	discovered *providerState
}

type providerState struct {
	provider *gooidc.Provider
	verifier *gooidc.IDTokenVerifier
}

func NewProvider(options ProviderOptions) (*Provider, error) {
	if !options.Config.Enabled || options.HTTPClient == nil ||
		options.HTTPClient.Timeout <= 0 || options.Now == nil {
		return nil, providerError(ProviderErrorConfiguration)
	}
	options.Config.Scopes = append([]string(nil), options.Config.Scopes...)
	return &Provider{
		config: options.Config, httpClient: options.HTTPClient, now: options.Now,
	}, nil
}

// AuthorizationURL returns an authorization URL bound to the transaction.
func (p *Provider) AuthorizationURL(
	ctx context.Context,
	transaction Transaction,
) (string, error) {
	discovered, err := p.discover(ctx)
	if err != nil {
		return "", err
	}
	options := []oauth2.AuthCodeOption{
		gooidc.Nonce(transaction.Nonce),
		oauth2.S256ChallengeOption(transaction.PKCEVerifier),
	}
	if transaction.Mode == TransactionModeReauth {
		options = append(
			options,
			oauth2.SetAuthURLParam("prompt", "login"),
			oauth2.SetAuthURLParam("max_age", "0"),
		)
	}
	return p.oauthConfig(discovered.provider).AuthCodeURL(
		transaction.State,
		options...,
	), nil
}

// Exchange exchanges a callback code and returns only verified ID-token claims.
func (p *Provider) Exchange(
	ctx context.Context,
	code string,
	transaction Transaction,
) ([]byte, error) {
	discovered, err := p.discover(ctx)
	if err != nil {
		return nil, err
	}
	clientContext := gooidc.ClientContext(ctx, p.httpClient)
	token, err := p.oauthConfig(discovered.provider).Exchange(
		clientContext,
		code,
		oauth2.VerifierOption(transaction.PKCEVerifier),
	)
	if err != nil {
		return nil, providerError(ProviderErrorExchange)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return nil, providerError(ProviderErrorToken)
	}
	idToken, err := discovered.verifier.Verify(clientContext, rawIDToken)
	if err != nil {
		return nil, providerError(ProviderErrorVerification)
	}
	if subtle.ConstantTimeCompare(
		[]byte(idToken.Nonce),
		[]byte(transaction.Nonce),
	) != 1 {
		return nil, providerError(ProviderErrorNonce)
	}
	var claims json.RawMessage
	if err := idToken.Claims(&claims); err != nil {
		return nil, providerError(ProviderErrorVerification)
	}
	return claims, nil
}

// ParseClaims maps verified claims using this provider's validated identity
// configuration without exposing credentials to callback callers.
func (p *Provider) ParseClaims(raw []byte) (ClaimIdentity, error) {
	return ParseClaims(raw, p.config.Issuer, GroupMapping{
		ClaimName: p.config.GroupClaim,
		Admin:     p.config.AdminGroup,
		Deployer:  p.config.DeployerGroup,
		Viewer:    p.config.ViewerGroup,
	}, p.config.AllowUnverifiedEmail)
}

// ParseReauthenticationClaims requires a current verified authentication from
// the OIDC provider for the transaction that initiated this flow.
func (p *Provider) ParseReauthenticationClaims(
	raw []byte,
	transaction Transaction,
) (ClaimIdentity, error) {
	if transaction.Mode != TransactionModeReauth {
		return ClaimIdentity{}, claimError("auth_time", ClaimInvalid)
	}
	identity, err := p.ParseClaims(raw)
	if err != nil {
		return ClaimIdentity{}, err
	}
	authenticatedAt, err := reauthenticationTime(raw)
	if err != nil {
		return ClaimIdentity{}, err
	}
	startedAt := transaction.ExpiresAt.Add(-transactionLifetime)
	if authenticatedAt.Before(startedAt) || authenticatedAt.After(p.now()) {
		return ClaimIdentity{}, claimError("auth_time", ClaimInvalid)
	}
	return identity, nil
}

// Issuer returns the configured issuer used to verify all provider claims.
func (p *Provider) Issuer() string {
	return p.config.Issuer
}

func (p *Provider) discover(ctx context.Context) (*providerState, error) {
	// ponytail: serialize first discovery; use singleflight only if concurrent
	// first-login latency becomes material.
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.discovered != nil {
		return p.discovered, nil
	}
	clientContext := gooidc.ClientContext(ctx, p.httpClient)
	provider, err := gooidc.NewProvider(clientContext, p.config.Issuer)
	if err != nil {
		return nil, providerError(ProviderErrorDiscovery)
	}
	p.discovered = &providerState{
		provider: provider,
		verifier: provider.VerifierContext(
			gooidc.ClientContext(context.Background(), p.httpClient),
			&gooidc.Config{
				ClientID: p.config.ClientID,
				Now:      p.now,
			},
		),
	}
	return p.discovered, nil
}

func (p *Provider) oauthConfig(provider *gooidc.Provider) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     p.config.ClientID,
		ClientSecret: p.config.ClientSecret,
		Endpoint:     provider.Endpoint(),
		RedirectURL:  p.config.CallbackURL,
		Scopes:       p.config.Scopes,
	}
}

func providerError(kind ProviderErrorKind) error {
	return &ProviderError{Kind: kind}
}

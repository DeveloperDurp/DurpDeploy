// Package oidctest provides a process-local TLS OpenID Connect test provider.
package oidctest

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultClientID     = "fixture-client"
	defaultClientSecret = "fixture-secret"
	defaultNonce        = "fixture-nonce-123456"
	defaultPKCEVerifier = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
)

// TokenMode controls the ID token returned by the fixture token endpoint.
type TokenMode uint8

const (
	TokenValid TokenMode = iota
	TokenMissing
	TokenNonString
	TokenBadSignature
	TokenWrongIssuer
	TokenWrongAudience
	TokenExpired
	TokenWrongNonce
)

// Claims are the mutable identity claims emitted in successful ID tokens.
type Claims struct {
	Subject       string
	Email         string
	EmailVerified bool
	Groups        []string
	AuthTime      time.Time
}

// Options configures a fixture. Zero values select deterministic test defaults.
type Options struct {
	Now          time.Time
	ClientID     string
	ClientSecret string
	CallbackURL  string
	Claims       Claims
}

// Counters records requests received by the fixture endpoints.
type Counters struct {
	Discovery int32
	Authorize int32
	Token     int32
	JWKS      int32
}

// AuthorizationRequest captures the latest authorization request without
// storing credentials or ID tokens.
type AuthorizationRequest struct {
	ClientID            string
	RedirectURL         string
	ResponseType        string
	State               string
	Nonce               string
	Scope               string
	Code                string
	CodeChallenge       string
	CodeChallengeMethod string
	Prompt              string
	MaxAge              string
}

// TokenRequest captures the latest token request without storing client secrets.
type TokenRequest struct {
	Code         string
	CodeVerifier string
}

// Capture contains the latest authorization and token request metadata.
type Capture struct {
	Authorization AuthorizationRequest
	Token         TokenRequest
}

// Fixture is a process-local TLS OIDC provider with mutable test controls.
type Fixture struct {
	server *httptest.Server
	client *http.Client
	now    time.Time

	clientID     string
	clientSecret string
	callbackURL  string
	initialCode  string

	mu         sync.RWMutex
	claims     Claims
	tokenMode  TokenMode
	privateKey *rsa.PrivateKey
	keyID      string
	keyVersion int
	nextCode   int
	codes      map[string]authorizationCode
	capture    Capture

	discoveryFailures atomic.Int32
	tokenFailures     atomic.Int32
	discoveryRequests atomic.Int32
	authorizeRequests atomic.Int32
	tokenRequests     atomic.Int32
	jwksRequests      atomic.Int32

	blockDiscovery       atomic.Bool
	discoveryStarted     chan struct{}
	discoveryStartedOnce sync.Once
	discoveryCanceled    chan struct{}
	discoveryCancelOnce  sync.Once
}

type authorizationCode struct {
	nonce         string
	codeChallenge string
	redeemed      bool
}

// New starts a TLS OIDC provider with generated signing and TLS keys.
func New(options Options) (*Fixture, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate fixture signing key: %w", err)
	}
	tlsCertificate, err := newTLSCertificate()
	if err != nil {
		return nil, err
	}
	if options.Now.IsZero() {
		options.Now = time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	}
	if options.ClientID == "" {
		options.ClientID = defaultClientID
	}
	if options.ClientSecret == "" {
		options.ClientSecret = defaultClientSecret
	}
	if options.CallbackURL == "" {
		options.CallbackURL = "https://deploy.example/login/oidc/callback"
	}
	if claimsAreZero(options.Claims) {
		options.Claims = Claims{
			Subject:       "fixture-subject",
			Email:         "fixture@example.test",
			EmailVerified: true,
			Groups:        []string{"fixture-admin"},
			AuthTime:      options.Now,
		}
	}

	fixture := &Fixture{
		now:               options.Now,
		clientID:          options.ClientID,
		clientSecret:      options.ClientSecret,
		callbackURL:       options.CallbackURL,
		claims:            cloneClaims(options.Claims),
		privateKey:        privateKey,
		keyID:             "fixture-key-1",
		keyVersion:        1,
		codes:             make(map[string]authorizationCode),
		discoveryStarted:  make(chan struct{}),
		discoveryCanceled: make(chan struct{}),
	}
	fixture.initialCode = fixture.newCodeLocked(
		defaultNonce,
		s256Challenge(defaultPKCEVerifier),
	)
	fixture.server = httptest.NewUnstartedServer(
		http.HandlerFunc(fixture.serveHTTP),
	)
	fixture.server.TLS = &tls.Config{
		Certificates: []tls.Certificate{tlsCertificate},
		MinVersion:   tls.VersionTLS12,
	}
	fixture.server.StartTLS()
	fixture.client = fixture.server.Client()
	transport, ok := fixture.client.Transport.(*http.Transport)
	if !ok {
		fixture.server.Close()
		return nil, fmt.Errorf(
			"fixture TLS client transport is %T, want *http.Transport",
			fixture.client.Transport,
		)
	}
	transport = transport.Clone()
	transport.DisableKeepAlives = true
	fixture.client.Transport = transport
	fixture.client.Timeout = time.Second
	return fixture, nil
}

// Close releases the fixture's trusted client connections and TLS listener.
func (f *Fixture) Close() {
	f.client.CloseIdleConnections()
	f.server.Close()
}

// URL returns the HTTPS issuer URL.
func (f *Fixture) URL() string {
	return f.server.URL
}

// Client returns the client that trusts only this fixture's TLS certificate.
func (f *Fixture) Client() *http.Client {
	return f.client
}

// Now returns the fixture's deterministic clock.
func (f *Fixture) Now() time.Time {
	return f.now
}

// ClientID returns the registered fixture client identifier.
func (f *Fixture) ClientID() string {
	return f.clientID
}

// ClientSecret returns the registered fixture client secret.
func (f *Fixture) ClientSecret() string {
	return f.clientSecret
}

// Code returns the unused initial authorization code.
func (f *Fixture) Code() string {
	return f.initialCode
}

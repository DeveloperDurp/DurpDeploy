package oidc

import (
	"context"
	"net/http"
	"testing"
)

type cachedVerifierContextKey struct{}

type jwksContextTransport struct {
	base   http.RoundTripper
	values chan<- any
}

func (t jwksContextTransport) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	if request.URL.Path == "/keys" {
		t.values <- request.Context().Value(cachedVerifierContextKey{})
	}
	return t.base.RoundTrip(request)
}

func TestProviderCachedVerifierDoesNotRetainRequestContextValues(t *testing.T) {
	// Given
	fixture := newProviderFixture(t)
	values := make(chan any, 2)
	fixture.client.Transport = jwksContextTransport{
		base: fixture.client.Transport, values: values,
	}
	provider := fixture.newProvider(t)
	requestContext := context.WithValue(
		context.Background(),
		cachedVerifierContextKey{},
		"session-bound-value",
	)
	if _, err := provider.Exchange(
		requestContext,
		fixture.authorizationCode,
		fixture.transaction,
	); err != nil {
		t.Fatalf("first Exchange() error = %v", err)
	}
	<-values
	fixture.rotateSigningKey(t)

	// When
	if _, err := provider.Exchange(
		context.Background(),
		fixture.NewCode(),
		fixture.transaction,
	); err != nil {
		t.Fatalf("rotated-key Exchange() error = %v", err)
	}

	// Then
	if retained := <-values; retained != nil {
		t.Fatal("cached verifier retained a request context value")
	}
}

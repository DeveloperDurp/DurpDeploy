package oidc

import (
	"context"
	"net/http"
	"sync"
	"testing"
	"time"
)

type blockingRoundTripper struct {
	started     chan struct{}
	canceled    chan struct{}
	release     chan struct{}
	releaseOnce sync.Once
}

func (transport *blockingRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	close(transport.started)
	select {
	case <-request.Context().Done():
		close(transport.canceled)
		return nil, request.Context().Err()
	case <-transport.release:
		return nil, context.Canceled
	}
}

func (transport *blockingRoundTripper) stop() {
	transport.releaseOnce.Do(func() { close(transport.release) })
}

func TestProviderAuthorizationURLCancelsInFlightTLSDiscovery(t *testing.T) {
	// Given
	fixture := newProviderFixture(t)
	discoveryStarted, discoveryCanceled := fixture.BlockDiscovery()
	provider := fixture.newProvider(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)

	// When
	go func() {
		_, err := provider.AuthorizationURL(ctx, fixture.transaction)
		result <- err
	}()
	select {
	case <-discoveryStarted:
	case <-time.After(time.Second):
		t.Fatal("TLS discovery did not reach fixture handler")
	}
	cancel()

	// Then
	var err error
	select {
	case err = <-result:
	case <-time.After(time.Second):
		t.Fatal("TLS discovery did not stop at caller cancellation")
	}
	assertProviderError(t, err, ProviderErrorDiscovery)
	select {
	case <-discoveryCanceled:
	case <-time.After(time.Second):
		t.Fatal("in-flight TLS discovery request was not canceled")
	}
}

func TestProviderAuthorizationURLStopsAtHTTPClientTimeout(t *testing.T) {
	// Given
	fixture := newProviderFixture(t)
	fixture.client.Timeout = 25 * time.Millisecond
	transport := &blockingRoundTripper{
		started:  make(chan struct{}),
		canceled: make(chan struct{}),
		release:  make(chan struct{}),
	}
	fixture.client.Transport = transport
	provider := fixture.newProvider(t)
	result := make(chan error, 1)

	// When
	go func() {
		_, err := provider.AuthorizationURL(
			context.Background(),
			fixture.transaction,
		)
		result <- err
	}()

	// Then
	var err error
	select {
	case err = <-result:
	case <-time.After(time.Second):
		transport.stop()
		t.Fatal("discovery did not stop at HTTP client timeout")
	}
	select {
	case <-transport.started:
	default:
		t.Fatal("discovery did not reach HTTP transport")
	}
	assertProviderError(t, err, ProviderErrorDiscovery)
	select {
	case <-transport.canceled:
	default:
		t.Fatal("slow discovery request context was not canceled")
	}
}

package notify

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
)

func TestNewHTTPClientDisablesProxy(t *testing.T) {
	// Given
	client := newHTTPClient(nil, nil)

	// When
	transport, ok := client.Transport.(*http.Transport)

	// Then
	if !ok {
		t.Fatalf("expected HTTP transport, got %T", client.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("expected proxy to be disabled")
	}
}

func TestNewHTTPClientDialsValidatedResolvedIP(t *testing.T) {
	// Given
	resolvedIP := net.IPv4(8, 8, 8, 8)
	dialErr := errors.New("dial failed")
	var lookupCalls int
	var lookupNetwork, lookupHost, dialAddress string
	client := newHTTPClient(
		func(_ context.Context, network, host string) ([]net.IP, error) {
			lookupCalls++
			lookupNetwork, lookupHost = network, host
			return []net.IP{resolvedIP}, nil
		},
		func(_ context.Context, _ string, address string) (net.Conn, error) {
			dialAddress = address
			return nil, dialErr
		},
	)
	transport := client.Transport.(*http.Transport)

	// When
	_, err := transport.DialContext(
		context.Background(),
		"tcp",
		"webhook.example:8443",
	)

	// Then
	if !errors.Is(err, dialErr) {
		t.Fatalf("expected dial error, got %v", err)
	}
	if lookupCalls != 1 ||
		lookupNetwork != "ip" ||
		lookupHost != "webhook.example" {
		t.Fatalf(
			"expected one lookup for webhook.example, got %d for %s %s",
			lookupCalls,
			lookupNetwork,
			lookupHost,
		)
	}
	if dialAddress != "8.8.8.8:8443" {
		t.Fatalf("expected validated IP dial address, got %q", dialAddress)
	}
}

func TestNewHTTPClientBlocksHostnameWithPrivateResolvedIP(t *testing.T) {
	// Given
	dialed := false
	client := newHTTPClient(
		func(_ context.Context, _, _ string) ([]net.IP, error) {
			return []net.IP{
				net.IPv4(8, 8, 8, 8),
				net.IPv4(10, 0, 0, 1),
			}, nil
		},
		func(_ context.Context, _, _ string) (net.Conn, error) {
			dialed = true
			return nil, nil
		},
	)
	transport := client.Transport.(*http.Transport)

	// When
	_, err := transport.DialContext(
		context.Background(),
		"tcp",
		"webhook.example:443",
	)

	// Then
	if !errors.Is(err, errBlockedNotificationHost) {
		t.Fatalf("expected blocked host error, got %v", err)
	}
	if dialed {
		t.Fatal("expected private resolution to prevent dialing")
	}
}

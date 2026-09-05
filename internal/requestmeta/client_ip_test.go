package requestmeta

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientIPIgnoresForwardingFromUntrustedPeer(t *testing.T) {
	// Given
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")

	// When
	got := clientIPThroughMiddleware(t, "198.51.100.0/24", request)

	// Then
	if got != "192.0.2.10" {
		t.Fatalf("client IP = %q, want direct peer", got)
	}
}

func TestClientIPUsesNearestUntrustedForwardedAddress(t *testing.T) {
	// Given
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Add(
		"X-Forwarded-For",
		"198.51.100.1, 203.0.113.9",
	)
	request.Header.Add("X-Forwarded-For", "192.0.2.11")

	// When
	got := clientIPThroughMiddleware(t, "192.0.2.0/24", request)

	// Then
	if got != "203.0.113.9" {
		t.Fatalf("client IP = %q, want nearest untrusted hop", got)
	}
}

func TestClientIPFallsBackToPeerForMalformedForwardingChain(t *testing.T) {
	// Given
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set(
		"X-Forwarded-For",
		"198.51.100.1, malformed, 192.0.2.11",
	)

	// When
	got := clientIPThroughMiddleware(t, "192.0.2.0/24", request)

	// Then
	if got != "192.0.2.10" {
		t.Fatalf("client IP = %q, want fail-closed direct peer", got)
	}
}

func TestClientIPUnmapsIPv4MappedIPv6Address(t *testing.T) {
	// Given
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "[::ffff:192.0.2.10]:1234"

	// When
	got := clientIPThroughMiddleware(t, "", request)

	// Then
	if got != "192.0.2.10" {
		t.Fatalf("client IP = %q, want unmapped IPv4", got)
	}
}

func TestClientIPReturnsUnknownForInvalidRemoteAddress(t *testing.T) {
	// Given
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "not an address"

	// When
	got := clientIPThroughMiddleware(t, "", request)

	// Then
	if got != "unknown" {
		t.Fatalf("client IP = %q, want unknown", got)
	}
}

func TestTrustedProxyPrefixesParsesAddressesAndCIDRs(t *testing.T) {
	// When
	prefixes := trustedProxyPrefixes(
		" 192.0.2.1, 2001:db8::1/64, invalid, ,",
	)

	// Then
	if len(prefixes) != 2 {
		t.Fatalf("prefix count = %d, want 2", len(prefixes))
	}
	if got := prefixes[0].String(); got != "192.0.2.1/32" {
		t.Fatalf("address prefix = %q, want 192.0.2.1/32", got)
	}
	if got := prefixes[1].String(); got != "2001:db8::/64" {
		t.Fatalf("CIDR prefix = %q, want 2001:db8::/64", got)
	}
}

func TestClientIPDoesNotDefaultToLoopbackWhenConfigIsInvalid(t *testing.T) {
	// Given
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")

	// When
	got := clientIPThroughMiddleware(t, "invalid", request)

	// Then
	if got != "127.0.0.1" {
		t.Fatalf("client IP = %q, want fail-closed loopback peer", got)
	}
}

func TestClientIPFallsBackToPeerWhenForwardingContainsOnlyProxies(
	t *testing.T,
) {
	// Given
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "192.0.2.11")

	// When
	got := clientIPThroughMiddleware(t, "192.0.2.0/24", request)

	// Then
	if got != "192.0.2.10" {
		t.Fatalf("client IP = %q, want direct peer", got)
	}
}

func clientIPThroughMiddleware(
	t *testing.T,
	trustedProxies string,
	request *http.Request,
) string {
	t.Helper()
	var got string
	handler := Middleware(trustedProxies)(http.HandlerFunc(
		func(_ http.ResponseWriter, r *http.Request) {
			got = ClientIP(r)
		},
	))
	handler.ServeHTTP(httptest.NewRecorder(), request)
	return got
}

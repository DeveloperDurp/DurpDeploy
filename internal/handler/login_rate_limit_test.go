package handler

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestLoginLimiterRecoversAfterWindow(t *testing.T) {
	now := time.Unix(1000, 0)
	limiter := &loginLimiter{
		entries: make(map[string]loginLimitEntry),
		now:     func() time.Time { return now },
	}
	for range loginPairLimit {
		if !limiter.allow("pair", loginPairLimit) {
			t.Fatal("request blocked before limit")
		}
	}
	if limiter.allow("pair", loginPairLimit) {
		t.Fatal("request allowed after limit")
	}
	now = now.Add(loginLimitWindow)
	if !limiter.allow("pair", loginPairLimit) {
		t.Fatal("request did not recover after window")
	}
}

func TestLoginPairKeyIsNormalizedAndBounded(t *testing.T) {
	first := loginPairKey(" Admin@Example.com ", "192.0.2.1")
	second := loginPairKey("admin@example.com", "192.0.2.1")
	if first != second {
		t.Fatal("equivalent account identifiers produced different keys")
	}
	large := loginPairKey(strings.Repeat("x", 10<<20), "192.0.2.1")
	if utf8.RuneCountInString(large) != len("login-pair:")+sha256.Size*2 {
		t.Fatalf("large identifier key length = %d", len(large))
	}
}

func TestLoginLimiterClientIPTrustsOnlyConfiguredProxies(t *testing.T) {
	request := httptest.NewRequest("POST", "/login", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set("X-Forwarded-For", "203.0.113.9")

	untrusted := newLoginLimiter()
	if got := untrusted.clientIP(request); got != "192.0.2.10" {
		t.Fatalf("untrusted forwarded IP = %q", got)
	}

	trusted := newLoginLimiter()
	trusted.trustedProxies = []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
	}
	if got := trusted.clientIP(request); got != "203.0.113.9" {
		t.Fatalf("trusted forwarded IP = %q", got)
	}
}

func TestLoginLimiterRejectsSpoofedLeftmostForwardedAddress(t *testing.T) {
	request := httptest.NewRequest("POST", "/login", nil)
	request.RemoteAddr = "192.0.2.10:1234"
	request.Header.Set(
		"X-Forwarded-For",
		"198.51.100.1, 203.0.113.9, 192.0.2.11",
	)
	limiter := newLoginLimiter()
	limiter.trustedProxies = []netip.Prefix{
		netip.MustParsePrefix("192.0.2.0/24"),
	}
	if got := limiter.clientIP(request); got != "203.0.113.9" {
		t.Fatalf("client IP = %q, want nearest untrusted hop", got)
	}
}

func TestTrustedProxyPrefixes(t *testing.T) {
	prefixes := trustedProxyPrefixes(
		" 192.0.2.1, 2001:db8::1/64, invalid, ,",
	)
	if len(prefixes) != 2 {
		t.Fatalf("prefix count = %d", len(prefixes))
	}
	if got := prefixes[0].String(); got != "192.0.2.1/32" {
		t.Fatalf("address prefix = %q", got)
	}
	if got := prefixes[1].String(); got != "2001:db8::/64" {
		t.Fatalf("masked prefix = %q", got)
	}
}

func TestLoginLimiterClientIPEdgeCases(t *testing.T) {
	limiter := newLoginLimiter()

	request := httptest.NewRequest("POST", "/login", nil)
	request.RemoteAddr = "not an address"
	if got := limiter.clientIP(request); got != "unknown" {
		t.Fatalf("invalid remote address = %q", got)
	}

	request.RemoteAddr = "127.0.0.1"
	request.Header.Set("X-Forwarded-For", "invalid, 127.0.0.2")
	if got := limiter.clientIP(request); got != "127.0.0.1" {
		t.Fatalf("proxy-only forwarding chain = %q", got)
	}
}

func TestLoginLimiterReset(t *testing.T) {
	limiter := newLoginLimiter()
	if !limiter.allow("key", 1) || limiter.allow("key", 1) {
		t.Fatal("unexpected limit behavior")
	}
	limiter.reset("key")
	if !limiter.allow("key", 1) {
		t.Fatal("reset key remained limited")
	}
}

func TestLoginRateLimitMiddleware(t *testing.T) {
	request := httptest.NewRequest("GET", "/login", nil)
	request.RemoteAddr = "192.0.2.10:1234"

	for _, test := range []struct {
		name       string
		middleware func(http.Handler) http.Handler
		limit      int
	}{
		{"mfa", newAuthHandlerForLimiter().MFARateLimit, mfaIPLimit},
		{"oidc", newAuthHandlerForLimiter().OIDCRateLimit, oidcIPLimit},
	} {
		t.Run(test.name, func(t *testing.T) {
			handler := test.middleware(
				http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(http.StatusNoContent)
				}),
			)
			for range test.limit {
				response := httptest.NewRecorder()
				handler.ServeHTTP(response, request)
				if response.Code != http.StatusNoContent {
					t.Fatalf("allowed status = %d", response.Code)
				}
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != http.StatusTooManyRequests {
				t.Fatalf("limited status = %d", response.Code)
			}
			if got := response.Header().Get("Retry-After"); got != "900" {
				t.Fatalf("Retry-After = %q", got)
			}
		})
	}
}

func newAuthHandlerForLimiter() *AuthHandler {
	return &AuthHandler{loginLimiter: newLoginLimiter()}
}

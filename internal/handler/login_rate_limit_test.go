package handler

import (
	"crypto/sha256"
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

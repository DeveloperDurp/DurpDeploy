package handler

import (
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
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

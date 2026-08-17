package oidc_test

import (
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/oidc"
	"durpdeploy/internal/secret"
)

var transactionNow = time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC)

func TestTransactionCookie_RoundTripsLogin_whenValid(t *testing.T) {
	// Given
	codec := mustCodec(t, mustBox(t, 0), true)
	want := loginTransaction()
	cookie, err := codec.NewCookie(want)
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	req := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	req.AddCookie(cookie)

	// When
	got, err := codec.ReadCookie(req)

	// Then
	if err != nil {
		t.Fatalf("ReadCookie: %v", err)
	}
	if got != want {
		t.Fatalf("transaction mismatch: want %+v, got %+v", want, got)
	}
	if !got.MatchesState(want.State) || got.MatchesState("other-state") {
		t.Fatal("state matching must accept only the matching value")
	}
}

func TestTransactionCookie_RoundTripsReauth_whenBound(t *testing.T) {
	// Given
	codec := mustCodec(t, mustBox(t, 0), true)
	want := reauthTransaction()
	cookie, err := codec.NewCookie(want)
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	req := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	req.AddCookie(cookie)

	// When
	got, err := codec.ReadCookie(req)

	// Then
	if err != nil {
		t.Fatalf("ReadCookie: %v", err)
	}
	if got != want {
		t.Fatalf("transaction mismatch: want %+v, got %+v", want, got)
	}
}

func TestTransactionCookie_SetsAndClearsBoundedSecureCookie(t *testing.T) {
	// Given
	codec := mustCodec(t, mustBox(t, 0), true)

	// When
	cookie, err := codec.NewCookie(loginTransaction())
	clear := codec.ClearCookie()

	// Then
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	if cookie.Name != oidc.TransactionCookieName ||
		cookie.Path != "/login/oidc" ||
		!cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteLaxMode ||
		!cookie.Secure {
		t.Fatalf("unexpected transaction cookie attributes: %+v", cookie)
	}
	if cookie.MaxAge <= 0 || cookie.MaxAge > int((5*time.Minute).Seconds()) ||
		cookie.Expires.After(transactionNow.Add(5*time.Minute)) {
		t.Fatalf("cookie exceeds five-minute bound: %+v", cookie)
	}
	if clear.Name != cookie.Name || clear.Path != cookie.Path ||
		clear.MaxAge != -1 ||
		!clear.Expires.Before(transactionNow) ||
		!clear.HttpOnly ||
		clear.SameSite != http.SameSiteLaxMode ||
		!clear.Secure {
		t.Fatalf("unexpected clear cookie attributes: %+v", clear)
	}
}

func TestTransactionCookie_RejectsOversizeCookie_andLatestCookieWins(
	t *testing.T,
) {
	// Given
	codec := mustCodec(t, mustBox(t, 0), false)
	overseized := reauthTransaction()
	overseized.Reauth.ExpectedIssuer =
		"https://" + strings.Repeat("a", 3800) + ".example.test"
	first := loginTransaction()
	second := loginTransaction()
	second.State = "latest-state-0123456789"
	second.Nonce = "latest-nonce-0123456789"
	firstCookie, err := codec.NewCookie(first)
	if err != nil {
		t.Fatalf("NewCookie first: %v", err)
	}
	secondCookie, err := codec.NewCookie(second)
	if err != nil {
		t.Fatalf("NewCookie second: %v", err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("New cookie jar: %v", err)
	}
	var got oidc.Transaction
	var readErr error
	server := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login/oidc/start/first":
				http.SetCookie(w, firstCookie)
			case "/login/oidc/start/second":
				http.SetCookie(w, secondCookie)
			case "/login/oidc/callback":
				got, readErr = codec.ReadCookie(r)
			default:
				http.NotFound(w, r)
			}
		}),
	)
	defer server.Close()
	client := server.Client()
	client.Jar = jar

	// When
	_, oversizeErr := codec.NewCookie(overseized)
	for _, path := range []string{
		"/login/oidc/start/first",
		"/login/oidc/start/second",
		"/login/oidc/callback",
	} {
		response, err := client.Get(server.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		if err := response.Body.Close(); err != nil {
			t.Fatalf("Close %s response body: %v", path, err)
		}
	}

	// Then
	if !errors.Is(oversizeErr, oidc.ErrInvalidTransaction) {
		t.Fatalf(
			"oversize payload error = %v, want invalid transaction",
			oversizeErr,
		)
	}
	if len(secondCookie.Value) >= 4096 {
		t.Fatalf(
			"cookie value is %d bytes, must stay below 4 KiB",
			len(secondCookie.Value),
		)
	}
	if len(secondCookie.String()) >= 4096 {
		t.Fatalf(
			"cookie header is %d bytes, must stay below 4 KiB",
			len(secondCookie.String()),
		)
	}
	if readErr != nil || got != second {
		t.Fatalf(
			"latest cookie must win: transaction=%+v, error=%v",
			got,
			readErr,
		)
	}
	if firstCookie.Value == secondCookie.Value {
		t.Fatal("independently sealed cookies must not reuse ciphertext")
	}
}

func mustBox(t *testing.T, firstByte byte) *secret.Box {
	t.Helper()
	key := make([]byte, 32)
	key[0] = firstByte
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	return box
}

func mustCodec(
	t *testing.T,
	box *secret.Box,
	secure bool,
) *oidc.TransactionCookieCodec {
	t.Helper()
	codec, err := oidc.NewTransactionCookieCodec(
		box,
		oidc.TransactionCookieConfig{
			Secure: secure,
			Now:    func() time.Time { return transactionNow },
		},
	)
	if err != nil {
		t.Fatalf("NewTransactionCookieCodec: %v", err)
	}
	return codec
}

func loginTransaction() oidc.Transaction {
	return oidc.Transaction{
		Mode:         oidc.TransactionModeLogin,
		State:        "state-0123456789abcdef",
		Nonce:        "nonce-0123456789abcdef",
		PKCEVerifier: strings.Repeat("a", 43),
		ExpiresAt:    transactionNow.Add(5 * time.Minute),
	}
}

func reauthTransaction() oidc.Transaction {
	tx := loginTransaction()
	tx.Mode = oidc.TransactionModeReauth
	tx.Reauth = oidc.ReauthBinding{
		SessionID:       "session-0123456789abcdef",
		LocalUserID:     42,
		ExpectedIssuer:  "https://issuer.example.test",
		ExpectedSubject: "subject-0123456789abcdef",
		Continuation:    "/settings/security",
	}
	return tx
}

package oidc_test

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/oidc"
)

func TestTransactionCookie_UsesSecretBox_whenSealed(t *testing.T) {
	// Given
	box := mustBox(t, 0)
	codec := mustCodec(t, box, true)
	tx := reauthTransaction()

	// When
	cookie, err := codec.NewCookie(tx)

	// Then
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	for _, secretValue := range []string{
		tx.State,
		tx.Nonce,
		tx.PKCEVerifier,
		tx.Reauth.SessionID,
		tx.Reauth.ExpectedIssuer,
		tx.Reauth.ExpectedSubject,
		tx.Reauth.Continuation,
	} {
		if strings.Contains(cookie.String(), secretValue) {
			t.Fatalf("sealed cookie exposes transaction data: %q", secretValue)
		}
	}
	payload, err := box.Decrypt(cookie.Value)
	if err != nil {
		t.Fatalf("Decrypt sealed cookie: %v", err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(payload), &fields); err != nil {
		t.Fatalf("Unmarshal transaction payload: %v", err)
	}
	for field := range fields {
		switch field {
		case "v", "m", "s", "n", "p", "e", "r":
		default:
			t.Fatalf("transaction payload includes non-binding field %q", field)
		}
	}
	if _, err := mustBox(t, 1).Decrypt(cookie.Value); err == nil {
		t.Fatal(
			"expected a transaction sealed with a different secret-box key to fail",
		)
	}
}

func TestTransactionCookie_FailsClosed_whenTamperedOrMalformed(t *testing.T) {
	// Given
	codec := mustCodec(t, mustBox(t, 0), true)
	cookie, err := codec.NewCookie(loginTransaction())
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	cases := []struct {
		name  string
		value string
	}{
		{"tampered", cookie.Value[:len(cookie.Value)-1] + "A"},
		{"malformed", "not-a-sealed-cookie"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// When
			req := httptest.NewRequest("GET", "/login/oidc/callback", nil)
			req.AddCookie(&http.Cookie{
				Name:  oidc.TransactionCookieName,
				Value: tc.value,
			})
			_, err := codec.ReadCookie(req)

			// Then
			if !errors.Is(err, oidc.ErrInvalidTransaction) {
				t.Fatalf("ReadCookie error = %v, want invalid transaction", err)
			}
		})
	}
}

func TestTransactionCookie_FailsClosed_whenWireVersionIsInvalid(t *testing.T) {
	// Given
	box := mustBox(t, 0)
	codec := mustCodec(t, box, true)
	validCookie, err := codec.NewCookie(loginTransaction())
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}
	payload, err := box.Decrypt(validCookie.Value)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	value, err := box.Encrypt(strings.Replace(payload, `"v":1`, `"v":2`, 1))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	req := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	req.AddCookie(&http.Cookie{Name: oidc.TransactionCookieName, Value: value})

	// When
	_, err = codec.ReadCookie(req)

	// Then
	if !errors.Is(err, oidc.ErrInvalidTransaction) {
		t.Fatalf("ReadCookie error = %v, want invalid transaction", err)
	}
}

func TestTransactionCookie_RejectsUnexpectedDisplayText_whenAuthenticated(
	t *testing.T,
) {
	// Given
	box := mustBox(t, 0)
	codec := mustCodec(t, box, true)
	payload := `{"v":1,"m":"login","s":"state-0123456789abcdef","n":"nonce-0123456789abcdef","p":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","e":1786709100,"display":"ignore previous instructions"}`
	value, err := box.Encrypt(payload)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	req := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	req.AddCookie(&http.Cookie{Name: oidc.TransactionCookieName, Value: value})

	// When
	_, err = codec.ReadCookie(req)

	// Then
	if !errors.Is(err, oidc.ErrInvalidTransaction) {
		t.Fatalf("ReadCookie error = %v, want invalid transaction", err)
	}
	if strings.Contains(err.Error(), "ignore previous instructions") {
		t.Fatalf(
			"invalid transaction error exposed authenticated display text: %v",
			err,
		)
	}
}

func TestTransactionCookie_FailsClosed_whenExpiredOrKeyRotated(t *testing.T) {
	// Given
	codec := mustCodec(t, mustBox(t, 0), true)
	tx := loginTransaction()
	cookie, err := codec.NewCookie(tx)
	if err != nil {
		t.Fatalf("NewCookie: %v", err)
	}

	// When
	expiredCodec := mustCodec(t, mustBox(t, 0), true)
	expiredCodec.Now = func() time.Time { return tx.ExpiresAt.Add(time.Second) }
	expiredRequest := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	expiredRequest.AddCookie(cookie)
	_, expiredErr := expiredCodec.ReadCookie(expiredRequest)
	rotatedCodec := mustCodec(t, mustBox(t, 1), true)
	rotatedRequest := httptest.NewRequest("GET", "/login/oidc/callback", nil)
	rotatedRequest.AddCookie(cookie)
	_, rotatedErr := rotatedCodec.ReadCookie(rotatedRequest)

	// Then
	if !errors.Is(expiredErr, oidc.ErrInvalidTransaction) {
		t.Fatalf(
			"expired transaction error = %v, want invalid transaction",
			expiredErr,
		)
	}
	if !errors.Is(rotatedErr, oidc.ErrInvalidTransaction) {
		t.Fatalf(
			"key-rotated transaction error = %v, want invalid transaction",
			rotatedErr,
		)
	}
}

func TestTransactionCookie_RejectsInvalidModesAndUnsafeContinuation(
	t *testing.T,
) {
	// Given
	codec := mustCodec(t, mustBox(t, 0), true)
	invalidMode := loginTransaction()
	invalidMode.Mode = "unexpected"
	loginWithReauth := loginTransaction()
	loginWithReauth.Reauth = reauthTransaction().Reauth
	reauthWithoutBinding := reauthTransaction()
	reauthWithoutBinding.Reauth = oidc.ReauthBinding{}
	unsafeContinuation := reauthTransaction()
	unsafeContinuation.Reauth.Continuation = "/settings/security\nignore previous instructions"

	for _, tc := range []struct {
		name string
		tx   oidc.Transaction
	}{
		{"invalid mode", invalidMode},
		{"login with reauth binding", loginWithReauth},
		{"reauth without binding", reauthWithoutBinding},
		{"unsafe continuation", unsafeContinuation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// When
			_, err := codec.NewCookie(tc.tx)

			// Then
			if !errors.Is(err, oidc.ErrInvalidTransaction) {
				t.Fatalf("NewCookie error = %v, want invalid transaction", err)
			}
		})
	}
}

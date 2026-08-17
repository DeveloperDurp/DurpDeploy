// Package oidc contains the browser-bound OIDC transaction primitives.
package oidc

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"durpdeploy/internal/secret"
)

const (
	transactionVersion   = 1
	transactionLifetime  = 5 * time.Minute
	maxCookieValueBytes  = 3800
	maxContinuationBytes = 1024

	// TransactionCookieName is the OIDC transaction cookie name.
	TransactionCookieName = "oidc_tx"
)

var ErrInvalidTransaction = errors.New("invalid OIDC transaction")

type TransactionMode string

const (
	TransactionModeLogin  TransactionMode = "login"
	TransactionModeReauth TransactionMode = "reauth"
)

// ReauthBinding prevents an OIDC reauthentication from switching local users.
type ReauthBinding struct {
	SessionID       string
	LocalUserID     int64
	ExpectedIssuer  string
	ExpectedSubject string
	Continuation    string
}

// Transaction holds only the short-lived OIDC authorization request bindings.
type Transaction struct {
	Mode         TransactionMode
	State        string
	Nonce        string
	PKCEVerifier string
	ExpiresAt    time.Time
	Reauth       ReauthBinding
}

// MatchesState compares callback state without leaking a prefix match.
func (t Transaction) MatchesState(state string) bool {
	return subtle.ConstantTimeCompare([]byte(t.State), []byte(state)) == 1
}

type TransactionCookieConfig struct {
	Secure bool
	Now    func() time.Time
}

type TransactionCookieCodec struct {
	box    *secret.Box
	secure bool
	Now    func() time.Time
}

type transactionWire struct {
	Version      int             `json:"v"`
	Mode         TransactionMode `json:"m"`
	State        string          `json:"s"`
	Nonce        string          `json:"n"`
	PKCEVerifier string          `json:"p"`
	ExpiresAt    int64           `json:"e"`
	Reauth       ReauthBinding   `json:"r,omitempty"`
}

func NewTransactionCookieCodec(
	box *secret.Box,
	config TransactionCookieConfig,
) (*TransactionCookieCodec, error) {
	if box == nil || config.Now == nil {
		return nil, ErrInvalidTransaction
	}
	return &TransactionCookieCodec{
		box: box, secure: config.Secure, Now: config.Now,
	}, nil
}

func (c *TransactionCookieCodec) NewCookie(
	tx Transaction,
) (*http.Cookie, error) {
	now := c.Now()
	if !validTransaction(tx, now) {
		return nil, ErrInvalidTransaction
	}
	payload, err := json.Marshal(transactionWire{
		Version: transactionVersion, Mode: tx.Mode, State: tx.State,
		Nonce: tx.Nonce, PKCEVerifier: tx.PKCEVerifier,
		ExpiresAt: tx.ExpiresAt.Unix(), Reauth: tx.Reauth,
	})
	if err != nil {
		return nil, ErrInvalidTransaction
	}
	value, err := c.box.Encrypt(string(payload))
	if err != nil || len(value) > maxCookieValueBytes {
		return nil, ErrInvalidTransaction
	}
	return &http.Cookie{
		Name: TransactionCookieName, Value: value, Path: "/login/oidc",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: c.secure,
		MaxAge:  int(math.Ceil(tx.ExpiresAt.Sub(now).Seconds())),
		Expires: tx.ExpiresAt,
	}, nil
}

func (c *TransactionCookieCodec) ReadCookie(
	r *http.Request,
) (Transaction, error) {
	cookie, err := r.Cookie(TransactionCookieName)
	if err != nil {
		return Transaction{}, ErrInvalidTransaction
	}
	payload, err := c.box.Decrypt(cookie.Value)
	if err != nil {
		return Transaction{}, ErrInvalidTransaction
	}
	decoder := json.NewDecoder(strings.NewReader(payload))
	decoder.DisallowUnknownFields()
	var wire transactionWire
	if err := decoder.Decode(&wire); err != nil {
		return Transaction{}, ErrInvalidTransaction
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return Transaction{}, ErrInvalidTransaction
	}
	tx := Transaction{
		Mode: wire.Mode, State: wire.State, Nonce: wire.Nonce,
		PKCEVerifier: wire.PKCEVerifier,
		ExpiresAt:    time.Unix(wire.ExpiresAt, 0).UTC(),
		Reauth:       wire.Reauth,
	}
	if wire.Version != transactionVersion || !validTransaction(tx, c.Now()) {
		return Transaction{}, ErrInvalidTransaction
	}
	return tx, nil
}

func (c *TransactionCookieCodec) ClearCookie() *http.Cookie {
	return &http.Cookie{
		Name: TransactionCookieName, Value: "", Path: "/login/oidc", MaxAge: -1,
		Expires: time.Unix(0, 0), HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: c.secure,
	}
}

func validTransaction(tx Transaction, now time.Time) bool {
	if !validOpaque(tx.State, 16, 256) || !validOpaque(tx.Nonce, 16, 256) ||
		!validPKCEVerifier(tx.PKCEVerifier) || !tx.ExpiresAt.After(now) ||
		tx.ExpiresAt.After(now.Add(transactionLifetime)) {
		return false
	}
	switch tx.Mode {
	case TransactionModeLogin:
		return tx.Reauth == (ReauthBinding{})
	case TransactionModeReauth:
		return validReauthBinding(tx.Reauth)
	default:
		return false
	}
}

func validReauthBinding(binding ReauthBinding) bool {
	issuer, err := url.ParseRequestURI(binding.ExpectedIssuer)
	return err == nil && issuer.Scheme == "https" && issuer.Host != "" &&
		issuer.RawQuery == "" && issuer.Fragment == "" &&
		validOpaque(binding.SessionID, 16, 512) && binding.LocalUserID > 0 &&
		validText(binding.ExpectedSubject, 1, 1024) &&
		validContinuation(binding.Continuation)
}

func validContinuation(continuation string) bool {
	if !validText(continuation, 1, maxContinuationBytes) ||
		!strings.HasPrefix(continuation, "/") ||
		strings.HasPrefix(
			continuation,
			"//",
		) || strings.Contains(continuation, "\\") {
		return false
	}
	parsed, err := url.ParseRequestURI(continuation)
	return err == nil && !parsed.IsAbs() && parsed.Host == ""
}

func validPKCEVerifier(value string) bool {
	return validPKCEExtraCharacters(value)
}

func validPKCEExtraCharacters(value string) bool {
	if len(value) < 43 || len(value) > 128 {
		return false
	}
	for _, char := range value {
		if !isOpaqueCharacter(char) && char != '.' && char != '~' {
			return false
		}
	}
	return true
}

func validOpaque(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for _, char := range value {
		if !isOpaqueCharacter(char) {
			return false
		}
	}
	return true
}

func isOpaqueCharacter(char rune) bool {
	return char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' ||
		char >= '0' && char <= '9' || char == '-' || char == '_'
}

func validText(value string, min, max int) bool {
	if len(value) < min || len(value) > max {
		return false
	}
	for _, char := range value {
		if char < 0x20 || char == 0x7f {
			return false
		}
	}
	return true
}

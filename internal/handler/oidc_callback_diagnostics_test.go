package handler_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/oidc/oidctest"
)

func TestOIDCCallback_logsOnlyClaimsStage_whenCallbackCarriesSensitiveValues(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	claims := oidctest.Claims{
		Subject:       "diagnostic-subject-sensitive",
		Email:         "diagnostic-email-sensitive@example.test",
		EmailVerified: true,
		Groups:        []string{"diagnostic-raw-claims-sensitive"},
	}
	fixture.provider.SetClaims(claims)
	flow := fixture.begin(t, h)
	transaction := readOIDCTransaction(
		t,
		flow.fixture.codec,
		flow.transactionCookie,
	)
	callback, err := url.Parse(flow.callbackURL)
	if err != nil {
		t.Fatalf("parse callback URL: %v", err)
	}
	query := callback.Query()
	code := query.Get("code")
	state := query.Get("state")
	providerErrorText := "diagnostic-upstream-provider-error-sensitive"
	callbackToken := "diagnostic-callback-token-sensitive"
	query.Set("error_description", providerErrorText)
	query.Set("token", callbackToken)
	callback.RawQuery = query.Encode()

	var output bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&output, nil)))
	t.Cleanup(func() {
		slog.SetDefault(previous)
	})

	// When
	response, err := doOIDCCallback(callback.String(), flow.transactionCookie)
	if err != nil {
		t.Fatalf("call sensitive OIDC callback: %v", err)
	}

	// Then
	assertOIDCCallbackFailure(t, response)
	assertOIDCStateCounts(t, h, [4]int{0, 0, 0, 0})

	logOutput := output.String()
	for _, sensitive := range []string{
		callback.RawQuery,
		code,
		state,
		transaction.Nonce,
		transaction.PKCEVerifier,
		claims.Groups[0],
		"fixture-access",
		fixture.provider.ClientSecret(),
		claims.Email,
		claims.Subject,
		providerErrorText,
		callbackToken,
	} {
		if strings.Contains(logOutput, sensitive) {
			t.Fatal("OIDC callback diagnostic leaked callback-sensitive data")
		}
	}

	decoder := json.NewDecoder(strings.NewReader(logOutput))
	var records []map[string]json.RawMessage
	for {
		var record map[string]json.RawMessage
		if err := decoder.Decode(&record); err == io.EOF {
			break
		} else if err != nil {
			t.Fatalf("decode OIDC callback diagnostic: %v", err)
		}
		records = append(records, record)
	}
	if len(records) != 1 {
		t.Fatalf("OIDC callback diagnostic records = %d, want 1", len(records))
	}
	for key := range records[0] {
		switch key {
		case "time", "level", "msg", "stage":
		default:
			t.Fatalf("OIDC callback diagnostic has unexpected field %q", key)
		}
	}
	var message string
	if err := json.Unmarshal(records[0]["msg"], &message); err != nil {
		t.Fatalf("decode OIDC callback diagnostic message: %v", err)
	}
	if message != "oidc initial login callback failed" {
		t.Fatalf("OIDC callback diagnostic message = %q", message)
	}
	var stage string
	if err := json.Unmarshal(records[0]["stage"], &stage); err != nil {
		t.Fatalf("decode OIDC callback diagnostic stage: %v", err)
	}
	if stage != "claims" {
		t.Fatalf("OIDC callback diagnostic stage = %q, want claims", stage)
	}
}

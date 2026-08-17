package handler_test

import (
	"net/url"
	"testing"

	"durpdeploy/internal/oidc/oidctest"
)

func TestOIDCCallback_rejectsVerifiedToken_whenTrustBindingFails(t *testing.T) {
	tests := []struct {
		name string
		mode oidctest.TokenMode
	}{
		{name: "issuer differs", mode: oidctest.TokenWrongIssuer},
		{name: "audience differs", mode: oidctest.TokenWrongAudience},
		{name: "signature is invalid", mode: oidctest.TokenBadSignature},
		{name: "token is expired", mode: oidctest.TokenExpired},
		{name: "nonce differs", mode: oidctest.TokenWrongNonce},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			fixture := configureOIDCCallback(t, h)
			flow := fixture.begin(t, h)
			fixture.provider.SetTokenMode(test.mode)

			// When
			response := flow.callback(t)

			// Then
			assertOIDCCallbackFailure(t, response)
			assertOIDCStateCounts(t, h, [4]int{0, 0, 0, 0})
		})
	}
}

func TestOIDCCallback_rejectsClaims_whenSubjectOrEmailIsInvalid(t *testing.T) {
	tests := []struct {
		name   string
		claims oidctest.Claims
	}{
		{
			name: "subject is empty",
			claims: oidctest.Claims{
				Email:         "subject@example.test",
				EmailVerified: true,
				Groups:        []string{"fixture-admin"},
			},
		},
		{
			name: "email is invalid",
			claims: oidctest.Claims{
				Subject:       "bad-email-subject",
				Email:         "not-an-email",
				EmailVerified: true,
				Groups:        []string{"fixture-admin"},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			fixture := configureOIDCCallback(t, h)
			fixture.provider.SetClaims(test.claims)
			flow := fixture.begin(t, h)

			// When
			response := flow.callback(t)

			// Then
			assertOIDCCallbackFailure(
				t,
				response,
				test.claims.Subject,
				test.claims.Email,
			)
			assertOIDCStateCounts(t, h, [4]int{0, 0, 0, 0})
		})
	}
}

func TestOIDCCallback_rejectsStateBeforeExchangingCode(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	flow := fixture.begin(t, h)
	callback, err := url.Parse(flow.callbackURL)
	if err != nil {
		t.Fatalf("parse callback URL: %v", err)
	}
	query := callback.Query()
	query.Set("state", "attacker-state")
	callback.RawQuery = query.Encode()

	// When
	response, err := doOIDCCallback(callback.String(), flow.transactionCookie)
	if err != nil {
		t.Fatalf("call mismatched-state callback: %v", err)
	}

	// Then
	assertOIDCCallbackFailure(t, response, "attacker-state")
	assertOIDCStateCounts(t, h, [4]int{0, 0, 0, 1})
	if got := fixture.provider.Counters().Token; got != 0 {
		t.Fatalf("token exchange requests = %d, want 0", got)
	}
}

func TestOIDCCallback_consumesTransaction_whenProviderReturnsError(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	flow := fixture.begin(t, h)
	callback, err := url.Parse(flow.callbackURL)
	if err != nil {
		t.Fatalf("parse callback URL: %v", err)
	}
	query := callback.Query()
	query.Del("code")
	query.Set("error", "access_denied")
	query.Set("error_description", "provider diagnostic fixture-secret")
	callback.RawQuery = query.Encode()

	// When
	response, err := doOIDCCallback(callback.String(), flow.transactionCookie)
	if err != nil {
		t.Fatalf("call provider-error callback: %v", err)
	}

	// Then
	assertOIDCCallbackFailure(
		t,
		response,
		"access_denied",
		"provider diagnostic fixture-secret",
	)
	assertOIDCStateCounts(t, h, [4]int{0, 0, 0, 0})
	if got := fixture.provider.Counters().Token; got != 0 {
		t.Fatalf("token exchange requests = %d, want 0", got)
	}
}

func TestOIDCCallback_consumesTransactionBeforeRejectingPKCEMismatch(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	flow := fixture.begin(t, h)
	badCookie := alteredPKCECookie(t, flow)

	// When
	response, err := doOIDCCallback(flow.callbackURL, badCookie)
	if err != nil {
		t.Fatalf("call PKCE-mismatch callback: %v", err)
	}

	// Then
	assertOIDCCallbackFailure(t, response)
	assertOIDCStateCounts(t, h, [4]int{0, 0, 0, 0})
	if got := fixture.provider.Counters().Token; got < 1 {
		t.Fatalf("token exchange requests = %d, want at least 1", got)
	}
}

func TestOIDCCallback_consumesTransactionBeforeRejectingExchangeFailure(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	flow := fixture.begin(t, h)
	fixture.provider.SetTokenFailures(1)

	// When
	response := flow.callback(t)

	// Then
	assertOIDCCallbackFailure(t, response)
	assertOIDCStateCounts(t, h, [4]int{0, 0, 0, 0})
	if got := fixture.provider.Counters().Token; got < 1 {
		t.Fatalf("token exchange requests = %d, want at least 1", got)
	}
}

func TestOIDCCallback_returnsGenericFailure_whenCodeIsMissing(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	flow := fixture.begin(t, h)
	callback, err := url.Parse(flow.callbackURL)
	if err != nil {
		t.Fatalf("parse callback URL: %v", err)
	}
	query := callback.Query()
	code := query.Get("code")
	query.Del("code")
	callback.RawQuery = query.Encode()

	// When
	response, err := doOIDCCallback(callback.String(), flow.transactionCookie)
	if err != nil {
		t.Fatalf("call missing-code callback: %v", err)
	}

	// Then
	assertOIDCCallbackFailure(t, response, code)
	assertOIDCStateCounts(t, h, [4]int{0, 0, 0, 0})
}

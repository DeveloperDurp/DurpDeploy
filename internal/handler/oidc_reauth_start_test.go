package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/oidc"
)

func TestOIDCReauthStart_requiresProtectedSession(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	server := startOIDCReauthServer(t, h)

	// When
	response := getOIDCReauthStart(t, server.URL, nil)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/login" {
		t.Fatalf(
			"unauthenticated OIDC reauthentication start = %d %q, want login redirect",
			response.StatusCode,
			response.Header.Get("Location"),
		)
	}
}

func TestOIDCReauthStart_bindsStoredIdentitySessionAndContinuation(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	current := seedSessionAs(t, h.repo, h.server, "admin@example.test", "admin")
	fixture := configureOIDCCallback(t, h)
	seedOIDCReauthIdentity(
		t,
		h,
		current.user.ID,
		fixture.provider.URL(),
		"fixture-subject",
	)

	// When
	flow := beginOIDCReauth(t, h, fixture, current)
	transaction := readOIDCTransaction(
		t,
		fixture.codec,
		flow.transactionCookie,
	)

	// Then
	if transaction.Mode != oidc.TransactionModeReauth {
		t.Fatalf("transaction mode = %q, want reauth", transaction.Mode)
	}
	wantBinding := oidc.ReauthBinding{
		SessionID:       current.sessionToken,
		LocalUserID:     current.user.ID,
		ExpectedIssuer:  fixture.provider.URL(),
		ExpectedSubject: "fixture-subject",
		Continuation:    "/settings/security",
	}
	if transaction.Reauth != wantBinding {
		t.Fatalf(
			"reauthentication binding = %#v, want %#v",
			transaction.Reauth,
			wantBinding,
		)
	}
	authorization := fixture.provider.Capture().Authorization
	if authorization.Prompt != "login" || authorization.MaxAge != "0" {
		t.Fatalf(
			"reauthentication authorization prompt/max_age = %q/%q, want login/0",
			authorization.Prompt,
			authorization.MaxAge,
		)
	}
	if got := localLoginStateCounts(t, h); got != [4]int{1, 1, 0, 1} {
		t.Fatalf(
			"OIDC reauthentication start state = %v, want one transaction only",
			got,
		)
	}
}

func TestOIDCReauthStart_rejectsUserWithoutStoredIdentity(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	current := seedSessionAs(t, h.repo, h.server, "admin@example.test", "admin")
	configureOIDCCallback(t, h)
	server := startOIDCReauthServer(t, h)

	// When
	response := getOIDCReauthStart(t, server.URL, current)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("missing identity start = %d, want 422", response.StatusCode)
	}
	if hasCookie(response.Cookies(), oidc.TransactionCookieName) {
		t.Fatal("missing identity start created an OIDC transaction cookie")
	}
	if got := localLoginStateCounts(t, h); got != [4]int{1, 1, 0, 0} {
		t.Fatalf(
			"missing identity start state = %v, want no OIDC transaction",
			got,
		)
	}
	if _, err := h.repo.Queries.GetUserByID(
		context.Background(),
		current.user.ID,
	); err != nil {
		t.Fatalf("load current user after rejected start: %v", err)
	}
}

func TestOIDCReauthPage_showsOIDCActionOnlyWhenConfigured(t *testing.T) {
	tests := []struct {
		name       string
		configure  bool
		wantAction bool
	}{
		{name: "configured", configure: true, wantAction: true},
		{name: "disabled", wantAction: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			h := newAuthHarness(t)
			current := seedSessionAs(
				t,
				h.repo,
				h.server,
				"admin@example.test",
				"admin",
			)
			if test.configure {
				configureOIDCCallback(t, h)
			}
			request := httptest.NewRequest(
				http.MethodGet,
				"/settings/security/reauth",
				nil,
			)
			request.AddCookie(&http.Cookie{
				Name: "session", Value: current.sessionToken,
			})
			response := httptest.NewRecorder()

			// When
			auth.AuthMiddleware(h.repo)(
				http.HandlerFunc(h.authHandler.SecurityReauthGet),
			).ServeHTTP(response, request)

			// Then
			if response.Code != http.StatusOK {
				t.Fatalf("reauthentication page = %d, want 200", response.Code)
			}
			hasAction := strings.Contains(
				response.Body.String(),
				`href="/settings/security/reauth/oidc"`,
			)
			if hasAction != test.wantAction {
				t.Fatalf(
					"OIDC action present = %t, want %t",
					hasAction,
					test.wantAction,
				)
			}
		})
	}
}

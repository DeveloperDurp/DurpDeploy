package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestOIDCReauthCallback_doesNotTreatLoginModeAsReauthentication(
	t *testing.T,
) {
	// Given
	h := newAuthHarness(t)
	current := seedSessionAs(t, h.repo, h.server, "admin@example.test", "admin")
	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{ID: current.sessionToken},
	); err != nil {
		t.Fatalf("make current session stale: %v", err)
	}
	fixture := configureOIDCCallback(t, h)
	seedOIDCReauthIdentity(
		t,
		h,
		current.user.ID,
		fixture.provider.URL(),
		"fixture-subject",
	)
	loginFlow := fixture.begin(t, h)
	callback, err := url.Parse(loginFlow.callbackURL)
	if err != nil {
		t.Fatalf("parse login callback URL: %v", err)
	}
	callbackServer := httptest.NewServer(auth.AuthMiddleware(h.repo)(
		http.HandlerFunc(h.authHandler.LoginOIDCCallbackGet),
	))
	t.Cleanup(callbackServer.Close)

	// When
	response, err := doOIDCReauthCallback(
		callbackServer.URL+callback.RequestURI(),
		loginFlow.transactionCookie,
		current,
	)
	if err != nil {
		t.Fatalf("call login-mode callback with active session: %v", err)
	}

	// Then
	assertOIDCCallbackSuccess(t, response)
	if readSession(
		t,
		h,
		current.sessionToken,
	).ReauthenticatedAt != (sql.NullInt64{}) {
		t.Fatal("login-mode callback was treated as OIDC reauthentication")
	}
}

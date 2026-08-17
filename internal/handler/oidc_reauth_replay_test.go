package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/mfa"
)

func TestOIDCReauthCallback_requiresActiveBoundSession(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	current := seedSessionAs(t, h.repo, h.server, "admin@example.test", "admin")
	if err := h.repo.Queries.MarkSessionReauthenticated(
		context.Background(),
		db.MarkSessionReauthenticatedParams{ID: current.sessionToken},
	); err != nil {
		t.Fatalf("make bound session stale: %v", err)
	}
	fixture := configureOIDCCallback(t, h)
	seedOIDCReauthIdentity(
		t,
		h,
		current.user.ID,
		fixture.provider.URL(),
		"fixture-subject",
	)
	flow := beginOIDCReauth(t, h, fixture, current)

	// When
	response := flow.callback(t, nil)
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != "/login" {
		t.Fatalf(
			"callback without active session = %d %q, want login redirect",
			response.StatusCode,
			response.Header.Get("Location"),
		)
	}
	if readSession(
		t,
		h,
		current.sessionToken,
	).ReauthenticatedAt != (sql.NullInt64{}) {
		t.Fatal("callback without active session refreshed the bound session")
	}
}

func TestOIDCReauthCallback_consumesTransactionOnlyOnce(t *testing.T) {
	// Given
	h := newAuthHarness(t)
	configureMFA(t, h, mfa.Config{})
	current := seedSessionAs(t, h.repo, h.server, "admin@example.test", "admin")
	fixture := configureOIDCCallback(t, h)
	seedOIDCReauthIdentity(
		t,
		h,
		current.user.ID,
		fixture.provider.URL(),
		"fixture-subject",
	)
	flow := beginOIDCReauth(t, h, fixture, current)

	// When
	first := flow.callback(t, current)
	second := flow.callback(t, current)

	// Then
	assertOIDCReauthSuccess(t, first, "/settings/security")
	assertOIDCCallbackFailure(t, second)
	if got := sessionCount(t, h, current.user.ID); got != 1 {
		t.Fatalf(
			"replayed OIDC reauthentication session count = %d, want 1",
			got,
		)
	}
	if got := fixture.provider.Counters().Token; got != 1 {
		t.Fatalf("replayed OIDC reauthentication exchanges = %d, want 1", got)
	}
}

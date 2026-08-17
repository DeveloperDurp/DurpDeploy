package server

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestOIDCOutage_LeavesOtherAuthenticationSurfacesUsable(t *testing.T) {
	// Given
	harness := newOIDCRouterHarness(t)
	fixture := harness.configureOIDC(t)
	app := harness.server(t, true)
	passwordHash, err := auth.HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	userID := harness.createUser(t, "fallback@example.test", passwordHash)
	sessionToken, csrfToken, err := auth.NewSessionToken()
	if err != nil {
		t.Fatalf("new session token: %v", err)
	}
	if _, err := harness.repo.Queries.CreateSession(
		context.Background(),
		db.CreateSessionParams{
			ID: sessionToken, UserID: userID, CsrfToken: csrfToken,
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		},
	); err != nil {
		t.Fatalf("create existing session: %v", err)
	}
	apiToken, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint API token: %v", err)
	}
	if _, err := harness.repo.Queries.CreateApiToken(
		context.Background(),
		db.CreateApiTokenParams{
			ID:          "oidc-outage-token",
			UserID:      userID,
			Name:        "OIDC outage test",
			TokenPrefix: prefix,
			TokenHash:   hash,
			Scope:       "global",
			ExpiresAt:   sql.NullInt64{},
		},
	); err != nil {
		t.Fatalf("create API token: %v", err)
	}
	fixture.SetDiscoveryFailures(1)

	// When
	client := oidcRouterClient(t)
	oidcResponse, err := client.Get(app.URL + "/login/oidc")
	if err != nil {
		t.Fatalf("start OIDC login during outage: %v", err)
	}
	defer oidcResponse.Body.Close()
	passwordResponse, err := client.PostForm(app.URL+"/login", url.Values{
		"email": {"fallback@example.test"}, "password": {"hunter2"},
	})
	if err != nil {
		t.Fatalf("password login after OIDC outage: %v", err)
	}
	defer passwordResponse.Body.Close()
	existingSession := oidcRouterClient(t)
	existingSession.Jar.SetCookies(mustParseURL(t, app.URL), []*http.Cookie{{
		Name: "session", Value: sessionToken,
	}})
	sessionResponse, err := existingSession.Get(app.URL + "/")
	if err != nil {
		t.Fatalf("get home with existing session: %v", err)
	}
	defer sessionResponse.Body.Close()
	healthResponse, err := client.Get(app.URL + "/healthz")
	if err != nil {
		t.Fatalf("get healthz during OIDC outage: %v", err)
	}
	defer healthResponse.Body.Close()
	apiRequest, err := http.NewRequestWithContext(
		context.Background(), http.MethodGet, app.URL+"/api/v1/projects", nil,
	)
	if err != nil {
		t.Fatalf("new bearer API request: %v", err)
	}
	apiRequest.Header.Set("Authorization", "Bearer "+apiToken)
	apiResponse, err := client.Do(apiRequest)
	if err != nil {
		t.Fatalf("call bearer API during OIDC outage: %v", err)
	}
	defer apiResponse.Body.Close()

	// Then
	if oidcResponse.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("OIDC outage status = %d, want 503", oidcResponse.StatusCode)
	}
	if passwordResponse.StatusCode != http.StatusSeeOther ||
		passwordResponse.Header.Get("Location") != "/" {
		t.Fatalf(
			"password fallback = %d %q, want 303 to /",
			passwordResponse.StatusCode,
			passwordResponse.Header.Get("Location"),
		)
	}
	if sessionResponse.StatusCode != http.StatusOK {
		t.Fatalf(
			"existing session status = %d, want 200",
			sessionResponse.StatusCode,
		)
	}
	if healthResponse.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", healthResponse.StatusCode)
	}
	if apiResponse.StatusCode != http.StatusOK {
		t.Fatalf("bearer API status = %d, want 200", apiResponse.StatusCode)
	}
}

func mustParseURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	return parsed
}

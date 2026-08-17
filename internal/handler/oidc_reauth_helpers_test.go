package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/oidc/oidctest"
)

type oidcReauthFlow struct {
	fixture           oidcCallbackFixture
	callbackURL       string
	transactionCookie *http.Cookie
}

func startOIDCReauthServer(t *testing.T, h *authHarness) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(auth.AuthMiddleware(h.repo)(
		http.HandlerFunc(h.authHandler.SecurityReauthOIDCGet),
	))
	t.Cleanup(server.Close)
	return server
}

func getOIDCReauthStart(
	t *testing.T,
	serverURL string,
	session *authedSession,
) *http.Response {
	t.Helper()
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		serverURL+"/settings/security/reauth/oidc",
		nil,
	)
	if err != nil {
		t.Fatalf("new OIDC reauthentication start request: %v", err)
	}
	if session != nil {
		request.AddCookie(&http.Cookie{
			Name: "session", Value: session.sessionToken,
		})
	}
	client := http.Client{
		Timeout: time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("start OIDC reauthentication: %v", err)
	}
	return response
}

func beginOIDCReauth(
	t *testing.T,
	h *authHarness,
	fixture oidcCallbackFixture,
	session *authedSession,
) oidcReauthFlow {
	t.Helper()
	startServer := startOIDCReauthServer(t, h)
	startResponse := getOIDCReauthStart(t, startServer.URL, session)
	defer startResponse.Body.Close()
	if startResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"OIDC reauthentication start = %d, want 303",
			startResponse.StatusCode,
		)
	}
	transactionCookie := oidcTransactionCookie(t, startResponse.Cookies())

	providerClient := *fixture.provider.Client()
	providerClient.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	authorizeResponse, err := providerClient.Get(
		startResponse.Header.Get("Location"),
	)
	if err != nil {
		t.Fatalf("authorize OIDC reauthentication: %v", err)
	}
	defer authorizeResponse.Body.Close()
	if authorizeResponse.StatusCode != http.StatusFound {
		t.Fatalf(
			"OIDC reauthentication authorize = %d, want 302",
			authorizeResponse.StatusCode,
		)
	}
	callback, err := url.Parse(authorizeResponse.Header.Get("Location"))
	if err != nil {
		t.Fatalf("parse OIDC reauthentication callback URL: %v", err)
	}
	callbackServer := httptest.NewServer(auth.AuthMiddleware(h.repo)(
		http.HandlerFunc(h.authHandler.LoginOIDCCallbackGet),
	))
	t.Cleanup(callbackServer.Close)
	return oidcReauthFlow{
		fixture:           fixture,
		callbackURL:       callbackServer.URL + callback.RequestURI(),
		transactionCookie: transactionCookie,
	}
}

func (f oidcReauthFlow) callback(
	t *testing.T,
	session *authedSession,
) *http.Response {
	t.Helper()
	response, err := doOIDCReauthCallback(
		f.callbackURL,
		f.transactionCookie,
		session,
	)
	if err != nil {
		t.Fatalf("call OIDC reauthentication callback: %v", err)
	}
	return response
}

func doOIDCReauthCallback(
	callbackURL string,
	transactionCookie *http.Cookie,
	session *authedSession,
) (*http.Response, error) {
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		callbackURL,
		nil,
	)
	if err != nil {
		return nil, err
	}
	request.Header.Set("User-Agent", oidcCallbackUserAgent)
	request.AddCookie(transactionCookie)
	if session != nil {
		request.AddCookie(&http.Cookie{
			Name: "session", Value: session.sessionToken,
		})
	}
	client := http.Client{
		Timeout: time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return client.Do(request)
}

func seedOIDCReauthIdentity(
	t *testing.T,
	h *authHarness,
	userID int64,
	issuer string,
	subject string,
) {
	t.Helper()
	if _, err := h.repo.Queries.CreateOIDCIdentity(
		context.Background(),
		db.CreateOIDCIdentityParams{
			Issuer: issuer, Subject: subject, UserID: userID,
		},
	); err != nil {
		t.Fatalf("create OIDC reauthentication identity: %v", err)
	}
}

func oidcReauthClaims(subject string, authTime time.Time) oidctest.Claims {
	return oidctest.Claims{
		Subject:       subject,
		Email:         "fixture@example.test",
		EmailVerified: true,
		Groups:        []string{"fixture-admin"},
		AuthTime:      authTime,
	}
}

func assertOIDCReauthSuccess(
	t *testing.T,
	response *http.Response,
	location string,
) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != location {
		t.Fatalf(
			"OIDC reauthentication response = %d %q, want 303 to %q",
			response.StatusCode,
			response.Header.Get("Location"),
			location,
		)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("successful OIDC reauthentication callback is cacheable")
	}
	if hasCookie(response.Cookies(), "session") {
		t.Fatal("OIDC reauthentication callback created a browser session")
	}
	if !oidcTransactionCookieCleared(response.Cookies()) {
		t.Fatal(
			"OIDC reauthentication callback did not clear transaction cookie",
		)
	}
}

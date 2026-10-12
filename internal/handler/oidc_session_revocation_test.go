package handler_test

import (
	"net/http"
	"testing"

	"durpdeploy/internal/oidc/oidctest"
)

func TestOIDCCallbackRoleChangeRevokesExistingSession(t *testing.T) {
	// Given: an administrator has an authenticated OIDC browser session.
	h := newAuthHarness(t)
	fixture := configureOIDCCallback(t, h)
	fixture.provider.SetClaims(oidctest.Claims{
		Subject: "role-change", Email: "role-change@example.test",
		EmailVerified: true, Groups: []string{"fixture-admin"},
	})
	response := fixture.begin(t, h).callback(t)
	assertOIDCCallbackSuccess(t, response)
	var oldSession *http.Cookie
	for _, cookie := range response.Cookies() {
		if cookie.Name == "session" {
			oldSession = cookie
		}
	}
	response.Body.Close()
	if oldSession == nil {
		t.Fatal("OIDC login did not issue a session")
	}
	assertSessionStatus(t, h.server+"/admin/users", oldSession, 200)
	// When: the provider removes administrator membership and login resyncs it.
	fixture.provider.SetClaims(oidctest.Claims{
		Subject: "role-change", Email: "role-change@example.test",
		EmailVerified: true, Groups: []string{"fixture-viewer"},
	})
	// Provider changes alone do not revoke the local session before login.
	assertSessionStatus(t, h.server+"/admin/users", oldSession, 200)
	response = fixture.begin(t, h).callback(t)
	defer response.Body.Close()
	assertOIDCCallbackSuccess(t, response)
	// Then: the old cookie no longer authenticates any protected page.
	assertSessionStatus(t, h.server+"/projects", oldSession, 303)
	for _, cookie := range response.Cookies() {
		if cookie.Name == "session" {
			assertSessionStatus(t, h.server+"/projects", cookie, 200)
			assertSessionStatus(t, h.server+"/admin/users", cookie, 403)
		}
	}
}

func assertSessionStatus(
	t *testing.T,
	address string,
	cookie *http.Cookie,
	want int,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), "GET", address, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(cookie)
	client := newJar(t)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != want {
		t.Fatalf("session response = %d, want %d", response.StatusCode, want)
	}
}

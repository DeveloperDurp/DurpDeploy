//go:build e2e

package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/config"
)

func TestLoginCrossOriginAPIWebE2E(t *testing.T) {
	f := newConfiguredBodyFixture(t, config.Defaults().Limits.HTTP)
	direct := httptest.NewTLSServer(f.server.Config.Handler)
	t.Cleanup(direct.Close)
	proxy := startBodyCaddy(t, f.server, direct.TLS.Certificates[0])
	attacker := seedAPIUser(t, f.h.repo, "attacker@example.test", "viewer")
	for mode, base := range map[string]string{"direct": direct.URL, "caddy": proxy} {
		t.Run(mode, func(t *testing.T) {
			client := bodyProxyClient(t, direct.Certificate(), 1)
			checkLoginOriginTransport(t, f, client, base, attacker.Email)
		})
	}
}

func checkLoginOriginTransport(
	t *testing.T, f *artifactE2E, client *http.Client, base, email string,
) {
	t.Helper()
	for _, authenticated := range []bool{false, true} {
		for _, metadata := range []struct{ site, origin string }{
			{"cross-site", "https://attacker.test"},
			{"same-site", base},
			{"", "https://attacker.test"},
		} {
			request := loginOriginPost(t, base, email)
			request.Header.Set("Sec-Fetch-Site", metadata.site)
			request.Header.Set("Origin", metadata.origin)
			checkLoginOriginRejectedTransport(
				t,
				f,
				client,
				request,
				authenticated,
			)
		}
	}
	for _, site := range []string{"same-origin", ""} {
		request := loginOriginPost(t, base, email)
		request.Header.Set("Sec-Fetch-Site", site)
		request.Header.Set("Origin", base)
		response, _ := transportLoginOriginResponse(t, client, request)
		if response.StatusCode != 303 {
			t.Fatalf("same-origin login = %d", response.StatusCode)
		}
	}
	request, err := http.NewRequestWithContext(t.Context(), "GET",
		base+"/api/v1/users/me", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+f.token)
	request.Header.Set("Origin", "https://attacker.test")
	request.Header.Set("Sec-Fetch-Site", "cross-site")
	response, body := transportLoginOriginResponse(t, client, request)
	var user struct{ Email string }
	if response.StatusCode != 200 || json.Unmarshal(body, &user) != nil ||
		user.Email != "configured-body@test.local" {
		t.Fatal("bearer identity contract changed with Origin headers")
	}
}

func checkLoginOriginRejectedTransport(
	t *testing.T, f *artifactE2E, client *http.Client,
	request *http.Request, authenticated bool,
) {
	t.Helper()
	before := loginOriginState(t, f)
	request.Header.Set("X-Forwarded-Host", request.URL.Host)
	request.Header.Set("X-Forwarded-Proto", "https")
	if authenticated {
		request.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	}
	response, body := transportLoginOriginResponse(t, client, request)
	if response.StatusCode != 403 || string(body) != "Forbidden\n" ||
		len(response.Cookies()) != 0 ||
		response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("cross-origin login = %d", response.StatusCode)
	}
	if after := loginOriginState(t, f); after != before {
		t.Fatalf("login changed state: %v -> %v", before, after)
	}
}

func loginOriginPost(t *testing.T, base, email string) *http.Request {
	t.Helper()
	form := url.Values{"email": {email}, "password": {"testpass"}}
	request, err := http.NewRequestWithContext(t.Context(), "POST",
		base+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func loginOriginState(t *testing.T, f *artifactE2E) [4]int {
	t.Helper()
	var state [4]int
	err := f.h.repo.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM sessions),
		(SELECT COUNT(*) FROM mfa_challenges),
		(SELECT COUNT(*) FROM audit_log WHERE action IN ('login', 'mfa_login_factor')),
		(SELECT COUNT(*) FROM users WHERE last_login_at IS NOT NULL)`).
		Scan(&state[0], &state[1], &state[2], &state[3])
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func transportLoginOriginResponse(
	t *testing.T, client *http.Client, request *http.Request,
) (*http.Response, []byte) {
	t.Helper()
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, body
}

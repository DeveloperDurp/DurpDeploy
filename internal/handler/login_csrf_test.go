package handler_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/mfa"
)

func TestLoginCrossOriginProtection(t *testing.T) {
	h := newAuthHarness(t)
	attacker := h.seedUser(t, "attacker@example.test", "testpass")
	enrolled := h.seedUser(t, "enrolled@example.test", "testpass")
	box := configureMFA(t, h, mfa.Config{})
	seedTOTP(t, h, enrolled, box)
	victim := seedSession(t, h.repo, h.server, "deployer")
	before := loginCSRFState(t, h)
	base, err := url.Parse(h.server)
	if err != nil {
		t.Fatal(err)
	}
	for _, metadata := range []struct{ name, site, origin string }{
		{"cross-site", "cross-site", "https://attacker.test"},
		{"same-site", "same-site", h.server},
		{"unknown-metadata", "unknown", h.server},
		{"metadata-precedes-origin", "cross-site", h.server},
		{"origin-only", "", "https://attacker.test"},
		{"sibling-origin", "", "http://sibling." + base.Host},
		{"different-port", "", "http://" + base.Hostname() + ":1"},
		{"null-origin", "", "null"},
		{"malformed-origin", "", "http://[invalid"},
	} {
		for _, credentials := range []struct{ name, email, password string }{
			{"password", attacker.Email, "testpass"},
			{"invalid-password", attacker.Email, "incorrect"},
			{"mfa", enrolled.Email, "testpass"},
		} {
			for _, authenticated := range []bool{false, true} {
				name := fmt.Sprintf("%s/%s/victim=%t",
					metadata.name, credentials.name, authenticated)
				t.Run(name, func(t *testing.T) {
					request := loginCSRFRequest(t, h.server,
						credentials.email, credentials.password)
					request.Header.Set("Sec-Fetch-Site", metadata.site)
					request.Header.Set("Origin", metadata.origin)
					request.Header.Set("X-Forwarded-Host", base.Host)
					request.Header.Set("X-Forwarded-Proto", "https")
					request.Header.Set("Forwarded", "host="+base.Host)
					request.Header.Set("HX-Request", "true")
					request.Header.Set("X-CSRF-Token", victim.csrfToken)
					checkLoginCSRFRejected(t, h, victim, authenticated,
						request, before)
				})
			}
		}
	}
	// More rejected attempts than the IP budget must not throttle a real login.
	response, err := victim.client.Get(h.server + "/settings/security")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal("victim session no longer accesses its account")
	}
	client := newJar(t)
	response, err = client.Do(loginCSRFRequest(t, h.server,
		attacker.Email, "testpass"))
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 || response.Header.Get("Retry-After") != "" {
		t.Fatalf("headerless login after rejections = %d", response.StatusCode)
	}
}

func checkLoginCSRFRejected(
	t *testing.T, h *authHarness, victim *authedSession,
	authenticated bool, request *http.Request, before [4]int,
) {
	t.Helper()
	client := newJar(t)
	if authenticated {
		client = *victim.client
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 403 ||
		string(body) != "Forbidden\n" {
		t.Fatalf("login = %d %q, err=%v", response.StatusCode, body, err)
	}
	if len(response.Cookies()) != 0 ||
		response.Header.Get("Cache-Control") != "no-store" ||
		response.Header.Get("Retry-After") != "" {
		t.Fatal("rejected login changed authentication state")
	}
	if after := loginCSRFState(t, h); after != before {
		t.Fatalf("state changed: %v -> %v", before, after)
	}
	if authenticated {
		cookies := client.Jar.Cookies(request.URL)
		if len(cookies) != 1 || cookies[0].Value != victim.sessionToken {
			t.Fatal("victim session was replaced")
		}
	}
}

func loginCSRFState(t *testing.T, h *authHarness) [4]int {
	t.Helper()
	var state [4]int
	err := h.repo.DB.QueryRowContext(t.Context(), `SELECT
		(SELECT COUNT(*) FROM sessions),
		(SELECT COUNT(*) FROM mfa_challenges),
		(SELECT COUNT(*) FROM audit_log),
		(SELECT COUNT(*) FROM users WHERE last_login_at IS NOT NULL)`).
		Scan(&state[0], &state[1], &state[2], &state[3])
	if err != nil {
		t.Fatal(err)
	}
	return state
}

func loginCSRFRequest(
	t *testing.T, base, email, password string,
) *http.Request {
	t.Helper()
	form := url.Values{"email": {email}, "password": {password}}
	request, err := http.NewRequestWithContext(t.Context(), "POST",
		base+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func TestLoginCrossOriginCompatibleRequests(t *testing.T) {
	for _, metadata := range []struct{ name, site, suffix string }{
		{"same-origin", "same-origin", ""},
		{"direct-navigation", "none", ""},
		{"origin-only", "", ""},
		{"legacy-path", "", "/path"},
		{"legacy-query", "", "?query=value"},
		{"legacy-fragment", "", "#fragment"},
		{"legacy-userinfo", "", ""},
		{"legacy-scheme", "", ""},
		{"metadata-precedes-origin", "same-origin", ""},
		{"headerless", "", ""},
	} {
		t.Run(metadata.name, func(t *testing.T) {
			h := newAuthHarness(t)
			user := h.seedUser(t, "login@example.test", "testpass")
			request := loginCSRFRequest(t, h.server, user.Email, "testpass")
			origin := h.server + metadata.suffix
			switch metadata.name {
			case "legacy-userinfo":
				origin = strings.Replace(origin, "://", "://user@", 1)
			case "legacy-scheme":
				origin = strings.Replace(origin, "http:", "https:", 1)
			case "metadata-precedes-origin":
				origin = "https://attacker.test"
			case "headerless":
				origin = ""
			}
			request.Header.Set("Sec-Fetch-Site", metadata.site)
			request.Header.Set("Origin", origin)
			client := newJar(t)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != 303 ||
				sessionCount(t, h, user.ID) != 1 {
				t.Fatalf("compatible login = %d", response.StatusCode)
			}
		})
	}
}

func TestLoginCrossOriginPreservesFormErrors(t *testing.T) {
	h := newAuthHarness(t)
	user := h.seedUser(t, "login@example.test", "testpass")
	for _, scenario := range []struct {
		name, body, site string
		status           int
	}{
		{"same-origin-invalid-password", "password=wrong", "same-origin", 422},
		{"malformed-cross-site", "password=%ZZ", "cross-site", 400},
		{"oversized-cross-site", "padding=" + strings.Repeat("a", 1<<20), "cross-site", 400},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			request := loginCSRFRequest(t, h.server, user.Email, "wrong")
			request.Body = io.NopCloser(strings.NewReader(scenario.body))
			request.ContentLength = int64(len(scenario.body))
			request.Header.Set("Sec-Fetch-Site", scenario.site)
			client := newJar(t)
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
			if response.StatusCode != scenario.status ||
				len(response.Cookies()) != 0 {
				t.Fatalf(
					"form response = %d, want %d",
					response.StatusCode,
					scenario.status,
				)
			}
		})
	}
}

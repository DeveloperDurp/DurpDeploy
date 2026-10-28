//go:build e2e && transportsecurity

package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"testing"
	"time"

	"durpdeploy/internal/mfa"
)

func transportField(t *testing.T, body []byte, name string) string {
	t.Helper()
	pattern := regexp.MustCompile(
		`name="` + regexp.QuoteMeta(name) + `" value="([^"]+)"`,
	)
	match := pattern.FindSubmatch(body)
	if len(match) != 2 {
		t.Fatal("security page omitted a required challenge field")
	}
	return string(match[1])
}

func TestTransportMFACookiesAPIWebE2E(t *testing.T) {
	f := loadTransportFixture(t)
	f.Email = "mfa@transport.test"
	data, err := json.Marshal(map[string]string{
		"email": f.Email, "password": f.Password, "role": "viewer",
		"name": "Transport MFA",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := transportRequest(
		t,
		"POST",
		f.URL+"/api/v1/admin/users",
		string(data),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+f.Token)
	transportResponse(t, f.client(t), req, 201)
	client := f.client(t)
	transportLogin(t, f, client)
	_, page := transportResponse(t, client,
		transportRequest(t, "GET", f.URL+"/settings/security", ""), 200)
	csrf := transportField(t, page, "csrf_token")
	_, begin := transportResponse(t, client, transportRequest(t, "POST",
		f.URL+"/settings/security/totp/begin",
		url.Values{"csrf_token": {csrf}}.Encode()), 200)
	key := regexp.MustCompile(`id="totp-manual-key"[^>]*>([^<]+)<`).
		FindSubmatch(begin)
	if len(key) != 2 {
		t.Fatal("TOTP enrollment omitted its private setup key")
	}
	code, err := mfa.GenerateTOTPCode(string(key[1]), time.Now())
	if err != nil {
		t.Fatal("could not generate the fixture TOTP code")
	}
	values := url.Values{"code": {code}}
	for _, name := range []string{"csrf_token", "challenge_token", "challenge_csrf"} {
		values.Set(name, transportField(t, begin, name))
	}
	transportResponse(t, client, transportRequest(t, "POST",
		f.URL+"/settings/security/totp/confirm", values.Encode()), 200)
	pending := transportLogin(t, f, client)
	if pending.Header.Get("Location") != "/login/mfa" {
		t.Fatal("MFA login unexpectedly created a final session")
	}
	challengeCSRF := checkTransportMFACookies(t, pending, false)
	res, _ := transportResponse(t, client, transportRequest(t, "POST",
		f.URL+"/login/mfa/cancel",
		url.Values{"csrf_token": {challengeCSRF}}.Encode()), 303)
	checkTransportMFACookies(t, res, true)
}

func checkTransportMFACookies(
	t *testing.T,
	res *http.Response,
	deleted bool,
) string {
	t.Helper()
	found := 0
	csrf := ""
	for _, c := range res.Cookies() {
		if c.Name != "mfa_pending" && c.Name != "mfa_csrf" {
			continue
		}
		found++
		if !c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode ||
			c.Path != "/" || c.Domain != "" || (deleted && c.MaxAge >= 0) ||
			(!deleted && !c.Expires.After(time.Now())) {
			t.Fatal("MFA cookie violates the HTTPS transport policy")
		}
		if c.Name == "mfa_csrf" {
			csrf = c.Value
		}
	}
	if found != 2 {
		t.Fatal("MFA response omitted its cookie pair")
	}
	return csrf
}

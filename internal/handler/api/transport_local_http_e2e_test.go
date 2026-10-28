//go:build e2e && transportsecurity

package api_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestTransportLocalHTTPDevelopmentAPIWebE2E(t *testing.T) {
	f := newPermissionFixture(t)
	seedAPIUser(t, f.h.repo, "http@transport.test", "admin")
	client := f.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	for _, path := range []string{"/login", "/api/v1/healthz"} {
		res, _ := transportResponse(t, client, transportRequest(t,
			"GET", f.server.URL+path, ""), 200)
		if res.Header.Get("Strict-Transport-Security") != "" ||
			res.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatal("local HTTP development changed its transport contract")
		}
	}
	form := url.Values{
		"email":    {"http@transport.test"},
		"password": {"testpass"},
	}
	req := transportRequest(t, "POST", f.server.URL+"/login", form.Encode())
	req.Header.Set("X-Forwarded-Proto", "https")
	res, _ := transportResponse(t, client, req, 303)
	if len(res.Cookies()) != 1 {
		t.Fatal("local login did not issue a session")
	}
	cookie := res.Cookies()[0]
	if cookie.Secure || !cookie.HttpOnly ||
		cookie.SameSite != http.SameSiteLaxMode {
		t.Fatal("forwarded input changed local HTTP cookie configuration")
	}
}

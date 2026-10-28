//go:build e2e && transportsecurity && framingsecurity

package api_test

import "testing"

func TestTransportFramingHeadersE2E(t *testing.T) {
	f := loadTransportFixture(t)
	for _, path := range []string{"/login", "/api/v1/healthz"} {
		res, _ := transportResponse(t, f.client(t),
			transportRequest(t, "GET", f.URL+path, ""), 200)
		if res.Header.Get(
			"Content-Security-Policy",
		) != "frame-ancestors 'self'" ||
			res.Header.Get("X-Frame-Options") != "SAMEORIGIN" {
			t.Fatal(
				"framing integration requires the policy from merged MR !66",
			)
		}
	}
}

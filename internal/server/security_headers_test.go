package server

import (
	"net/http/httptest"
	"testing"
)

func TestContentTypePolicyOnPublicAPIAndWeb(t *testing.T) {
	h := newOIDCRouterHarness(t)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	for _, path := range []string{
		"/api/v1/healthz", "/api/v1/projects", "/login", "/missing",
	} {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("missing content type policy")
			}
			if rec.Header().Get("Strict-Transport-Security") != "" {
				t.Fatal("direct HTTP must not set proxy HTTPS policy")
			}
		})
	}
}

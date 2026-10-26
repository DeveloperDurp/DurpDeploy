package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBrowserFramingHTTP(t *testing.T) {
	h := newOIDCRouterHarness(t)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	router.Get("/framing-error", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "framing test internal error", 500)
	})
	router.Get("/framing-panic", func(http.ResponseWriter, *http.Request) {
		panic("framing test panic")
	})
	s := httptest.NewServer(router)
	t.Cleanup(s.Close)
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	for _, scenario := range []struct {
		method, path string
		status       int
	}{
		{"GET", "/login", 200},
		{"GET", "/projects", 303},
		{"GET", "/missing-page", 404},
		{"POST", "/favicon.ico", 405},
		{"GET", "/api/v1/projects", 401},
		{"GET", "/api/v1/healthz", 200},
		{"GET", "/framing-error", 500},
		{"GET", "/framing-panic", 500},
	} {
		t.Run(scenario.method+scenario.path, func(t *testing.T) {
			request, err := http.NewRequestWithContext(
				t.Context(), scenario.method, s.URL+scenario.path, nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != scenario.status ||
				response.Header.Get(
					"Content-Security-Policy",
				) != "frame-ancestors 'self'" ||
				response.Header.Get("X-Frame-Options") != "SAMEORIGIN" {
				t.Fatalf(
					"status=%d headers=%v",
					response.StatusCode,
					response.Header,
				)
			}
		})
	}
}

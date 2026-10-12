package server

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/static"
)

func TestStaticAssetCachingHTTP(t *testing.T) {
	// Given the real router and its embedded production bundles.
	h := newOIDCRouterHarness(t)
	s := httptest.NewServer(
		NewRouter(h.repo, h.runner, h.parser, h.authHandler),
	)
	defer s.Close()
	for _, name := range []string{"css/tailwind.min.css", "js/app.bundle.js"} {
		t.Run(name, func(t *testing.T) {
			// When a client fetches the versioned asset.
			path := static.URL(name)
			response, err := http.Get(s.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			// Then its identity follows its content and it is safe to cache.
			parsed, err := url.Parse(path)
			if err != nil {
				t.Fatal(err)
			}
			hash := fmt.Sprintf("%x", sha256.Sum256(body))
			if response.StatusCode != 200 || parsed.Query().Get("v") != hash ||
				response.Header.Get(
					"Cache-Control",
				) != "public, max-age=31536000, immutable" ||
				response.Header.Get("ETag") != `"`+hash+`"` {
				t.Fatalf(
					"asset status=%d headers=%v",
					response.StatusCode,
					response.Header,
				)
			}
			request, err := http.NewRequest("GET", s.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("If-None-Match", response.Header.Get("ETag"))
			conditional, err := http.DefaultClient.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			conditional.Body.Close()
			if conditional.StatusCode != 304 {
				t.Fatalf("conditional=%d", conditional.StatusCode)
			}
			for _, test := range []struct {
				path   string
				status int
				cache  string
			}{
				{"/static/" + name, 200, "no-cache"},
				{"/static/" + name + "?v=old-build", 404, "no-store"},
			} {
				response, err := http.Get(s.URL + test.path)
				if err != nil {
					t.Fatal(err)
				}
				response.Body.Close()
				if response.StatusCode != test.status ||
					response.Header.Get("Cache-Control") != test.cache {
					t.Fatalf(
						"%s status=%d cache=%s",
						test.path,
						response.StatusCode,
						response.Header.Get("Cache-Control"),
					)
				}
			}
		})
	}
	response, err := http.Get(s.URL + "/login")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if strings.Contains(response.Header.Get("Cache-Control"), "immutable") {
		t.Fatal("HTML inherited asset caching")
	}
}

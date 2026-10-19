package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

func newSwaggerTLSProxy(t *testing.T, backend string) *httptest.Server {
	t.Helper()
	target, err := url.Parse(backend)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewTLSServer(httputil.NewSingleHostReverseProxy(target))
	t.Cleanup(proxy.Close)
	return proxy
}

func TestSwaggerInstanceAPIE2E(t *testing.T) {
	f := newPermissionFixture(t)
	proxy := newSwaggerTLSProxy(t, f.server.URL)
	for _, instance := range []*httptest.Server{f.server, proxy} {
		t.Run(instance.URL, func(t *testing.T) {
			checkSwaggerInstance(t, instance)
		})
	}
}

func swaggerInstanceGet(
	t *testing.T,
	instance *httptest.Server,
	path string,
	status int,
) []byte {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "GET",
		instance.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Forwarded headers must not supply a different API target.
	req.Header.Set("X-Forwarded-Host", "other.invalid:8080")
	req.Header.Set("X-Forwarded-Proto", "http")
	res, err := instance.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != status {
		t.Fatalf("%s: status %d, want %d", path,
			res.StatusCode, status)
	}
	return body
}

func checkSwaggerInstance(t *testing.T, instance *httptest.Server) {
	t.Helper()
	var spec struct {
		Host, BasePath string
		Schemes        []string
		Paths          map[string]map[string]struct{ Schemes []string }
	}
	for _, path := range []string{"/api/swagger/", "/api/swagger/index.html"} {
		if !strings.Contains(
			string(swaggerInstanceGet(t, instance, path, 200)),
			"SwaggerUIBundle({",
		) {
			t.Fatalf("%s did not render Swagger UI", path)
		}
	}
	if err := json.Unmarshal(swaggerInstanceGet(t, instance, "/api/swagger/spec", 200), &spec); err != nil {
		t.Fatal(err)
	}
	if spec.Host != "" || len(spec.Schemes) != 0 ||
		spec.BasePath != "/api/v1" {
		t.Fatalf(
			"spec overrides its origin: host=%q schemes=%v base=%q",
			spec.Host,
			spec.Schemes,
			spec.BasePath,
		)
	}
	for path, operations := range spec.Paths {
		for method, operation := range operations {
			if len(operation.Schemes) != 0 {
				t.Errorf("%s %s overrides scheme: %v",
					method, path, operation.Schemes)
			}
		}
	}
	var health struct{ OK bool }
	if err := json.Unmarshal(swaggerInstanceGet(t, instance, spec.BasePath+"/healthz", 200),
		&health); err != nil ||
		!health.OK {
		t.Fatalf("health response: ok=%v err=%v", health.OK, err)
	}
	swaggerInstanceGet(t, instance, spec.BasePath+"/projects", 401)
}

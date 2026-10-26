//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"testing"
)

type framingRequest struct {
	path, site    string
	authenticated bool
}

type framingE2E struct {
	*artifactE2E
	viewer, viewerToken string
	frames              chan framingRequest
}

type framingScenario struct {
	name, method, path, identity, body string
	status                             int
}

func framingParent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	source := html.EscapeString(r.URL.Query().Get("src"))
	fmt.Fprintf(
		w,
		`<!doctype html><title>Framing check</title><h1>Framing check</h1><a href="%s">Open page</a><iframe id="target" title="DurpDeploy" src="%s" onload="document.body.dataset.loaded='true'" style="width:95vw;height:75vh"></iframe>`,
		source,
		source,
	)
}

func newFramingE2E(t *testing.T) *framingE2E {
	t.Helper()
	f := newArtifactE2E(t, fakePodman(t))
	viewer := seedAPIUser(t, f.h.repo, "framing-viewer@example.test", "viewer")
	_, viewerToken := seedAPIToken(t, f.h.repo, viewer.ID)
	result := &framingE2E{
		artifactE2E: f, viewer: memberRoleSession(t, f.h, viewer.ID),
		viewerToken: viewerToken, frames: make(chan framingRequest, 32),
	}
	router := f.server.Config.Handler
	f.server.Config.Handler = http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/framing-parent" {
				framingParent(w, r)
				return
			}
			if r.Header.Get("Sec-Fetch-Dest") == "iframe" {
				cookie, err := r.Cookie("session")
				seen := framingRequest{
					path: r.URL.Path,
					site: r.Header.Get("Sec-Fetch-Site"),
				}
				seen.authenticated = err == nil &&
					(cookie.Value == f.session || cookie.Value == result.viewer)
				select {
				case result.frames <- seen:
				default:
				}
			}
			router.ServeHTTP(w, r)
		},
	)
	return result
}

func assertFramingHeaders(t *testing.T, header http.Header) {
	t.Helper()
	for name, value := range map[string]string{
		"Content-Security-Policy": "frame-ancestors 'self'",
		"X-Frame-Options":         "SAMEORIGIN",
	} {
		if len(header.Values(name)) != 1 || header.Get(name) != value {
			t.Fatalf("framing header %s=%q", name, header.Values(name))
		}
	}
}

func TestFramingPolicyAPIWebE2E(t *testing.T) {
	f := newFramingE2E(t)
	upstream, err := url.Parse(f.baseURL)
	if err != nil {
		t.Fatal(err)
	}
	proxy := httptest.NewServer(httputil.NewSingleHostReverseProxy(upstream))
	t.Cleanup(proxy.Close)
	for mode, address := range map[string]string{"direct": f.baseURL, "proxy": proxy.URL} {
		for _, scenario := range []framingScenario{
			{"login", "GET", "/login", "", "", 200},
			{"admin", "GET", "/projects", "admin", "", 200},
			{"viewer", "GET", "/projects", "viewer", "", 200},
			{"htmx", "GET", "/projects", "admin", "", 200},
			{"redirect", "GET", "/projects", "", "", 303},
			{"csrf", "POST", "/projects", "admin", "name=blocked", 403},
			{"viewer-write", "POST", "/projects", "viewer", "name=blocked", 403},
			{"api-read", "GET", "/api/v1/projects", "api", "", 200},
			{"api-auth", "GET", "/api/v1/projects", "", "", 401},
			{"api-invalid", "POST", "/api/v1/projects", "api", `{"name":""}`, 400},
			{"api-viewer-write", "POST", "/api/v1/projects", "viewer-api", `{"name":"blocked"}`, 403},
			{"not-found", "GET", "/missing-page", "", "", 404},
			{"method", "POST", "/favicon.ico", "", "", 405},
		} {
			t.Run(mode+"/"+scenario.name, func(t *testing.T) {
				f.checkFramingResponse(t, address, scenario)
			})
		}
	}
}

func (f *framingE2E) checkFramingResponse(
	t *testing.T, address string, scenario framingScenario,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(), scenario.method, address+scenario.path,
		strings.NewReader(scenario.body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if strings.HasPrefix(scenario.path, "/api/") {
		request.Header.Set("Content-Type", "application/json")
	}
	switch scenario.identity {
	case "admin":
		request.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	case "viewer":
		request.AddCookie(&http.Cookie{Name: "session", Value: f.viewer})
	case "api":
		request.Header.Set("Authorization", "Bearer "+f.token)
	case "viewer-api":
		request.Header.Set("Authorization", "Bearer "+f.viewerToken)
	}
	if scenario.name == "htmx" {
		request.Header.Set("HX-Request", "true")
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != scenario.status {
		t.Fatalf("status=%d error=%v body=%s", response.StatusCode, err, body)
	}
	assertFramingHeaders(t, response.Header)
	if strings.HasPrefix(scenario.path, "/api/") && !json.Valid(body) {
		t.Fatalf("API contract lost JSON response: %s", body)
	}
}

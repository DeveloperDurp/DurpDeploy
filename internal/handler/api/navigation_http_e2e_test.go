//go:build e2e

package api_test

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPageNavigationHTTPAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	release := seedRelease(t, f.h.repo, f.project.ID)
	deployment := seedDeployment(
		t,
		f.h.repo,
		release.ID,
		f.environment.ID,
		"succeeded",
	)
	f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", f.project.ID), nil, 200)
	f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
		nil,
		200,
	)
	for _, path := range []string{
		"/projects", fmt.Sprintf("/projects/%d", f.project.ID),
		fmt.Sprintf("/projects/%d/releases", f.project.ID),
		fmt.Sprintf("/projects/%d/variables", f.project.ID),
		fmt.Sprintf("/projects/%d/schedules", f.project.ID),
		"/templates", "/environments/new", "/deployments",
		fmt.Sprintf("/deployments/%d", deployment.ID),
	} {
		for _, navigation := range []bool{false, true} {
			request, err := http.NewRequest("GET", f.baseURL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.AddCookie(&http.Cookie{Name: "session", Value: f.session})
			request.Header.Set("HX-Request", "true")
			if navigation {
				request.Header.Set("HX-Boosted", "true")
			}
			response, err := f.client.Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			full := strings.Contains(string(body), `id="page-content"`)
			if response.StatusCode != 200 || full != navigation ||
				response.Header.Get("Cache-Control") != "no-store" {
				t.Fatalf(
					"%s boosted=%t status=%d full=%t cache=%s",
					path,
					navigation,
					response.StatusCode,
					full,
					response.Header.Get("Cache-Control"),
				)
			}
		}
	}
	for _, test := range []struct {
		header string
		status int
	}{{"HX-Boosted", 200}, {"HX-History-Restore-Request", 401}} {
		request, err := http.NewRequest("GET", f.baseURL+"/projects", nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(test.header, "true")
		response, err := f.client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status ||
			response.Header.Get("HX-Redirect") != "/login" {
			t.Fatalf(
				"unauthenticated %s status=%d redirect=%s",
				test.header,
				response.StatusCode,
				response.Header.Get("HX-Redirect"),
			)
		}
	}
}

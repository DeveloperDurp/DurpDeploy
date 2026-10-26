//go:build e2e

package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

func TestLoginDestinationAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	path := fmt.Sprintf("/projects/%d?source=login-context", f.project.ID)
	client := loginContextClient(t)
	response, err := client.Get(f.baseURL + path)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	location := response.Header.Get("Location")
	if response.StatusCode != 303 ||
		location != "/login?"+(url.Values{"next": {path}}).Encode() {
		t.Fatalf(
			"protected page redirect = %d %q",
			response.StatusCode,
			location,
		)
	}
	page, err := client.Get(f.baseURL + location)
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("login context page can be cached")
	}
	response, err = client.PostForm(f.baseURL+"/login", url.Values{
		"email": {
			"artifact-e2e@example.com",
		}, "password": {"wrong-test-password"},
	})
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 422 ||
		!strings.Contains(string(body), `value="artifact-e2e@example.com"`) ||
		strings.Contains(string(body), "wrong-test-password") {
		t.Fatal("login error failed to retain only non-secret input")
	}
	response = loginContextPost(
		t,
		client,
		f.baseURL,
		"artifact-e2e@example.com",
	)
	if response.StatusCode != 303 || response.Header.Get("Location") != path {
		t.Fatal("corrected login did not return to the original page")
	}
	response, err = client.Get(f.baseURL + path)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("returned project status = %d", response.StatusCode)
	}
	f.api(t, "GET", fmt.Sprintf("/api/v1/projects/%d", f.project.ID), nil, 200)
	checkLoginDestinationDenied(t, f, path)
}

func loginContextClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Client{Jar: jar,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}}
}

func loginContextPost(
	t *testing.T,
	client *http.Client,
	base, email string,
) *http.Response {
	t.Helper()
	response, err := client.PostForm(base+"/login", url.Values{
		"email": {email}, "password": {"testpass"},
	})
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response
}

func checkLoginDestinationDenied(t *testing.T, f *artifactE2E, path string) {
	t.Helper()
	user := seedAPIUser(
		t,
		f.h.repo,
		"context-outsider@example.test",
		"maintainer",
	)
	client := loginContextClient(t)
	page, err := client.Get(
		f.baseURL + "/login?" + url.Values{"next": {path}}.Encode(),
	)
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	response := loginContextPost(t, client, f.baseURL, user.Email)
	if response.Header.Get("Location") != path {
		t.Fatal("local destination changed")
	}
	response, err = client.Get(f.baseURL + path)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf(
			"login destination bypassed project access: %d",
			response.StatusCode,
		)
	}
}

func TestLoginDestinationHTMXAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	for _, scenario := range []loginRedirectCase{
		{"boosted", "GET", "/projects", "", map[string]string{"HX-Request": "true", "HX-Boosted": "true"}, 200},
		{"history", "GET", "/environments", "", map[string]string{"HX-History-Restore-Request": "true"}, 401},
		{"fragment", "GET", "/projects/1/steps", f.baseURL + "/projects?name=context", map[string]string{"HX-Request": "true"}, 200},
		{"expired-write", "POST", "/projects", f.baseURL + "/projects/1", map[string]string{"HX-Request": "true"}, 200},
		{"external-current", "GET", "/projects/1/steps", "https://evil.test/", map[string]string{"HX-Request": "true"}, 200},
		{"native-write", "POST", "/projects", "", nil, 303},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			checkLoginRedirectScenario(t, f, scenario)
		})
	}
}

type loginRedirectCase struct {
	name, method, path, current string
	headers                     map[string]string
	status                      int
}

func checkLoginRedirectScenario(
	t *testing.T,
	f *artifactE2E,
	scenario loginRedirectCase,
) {
	t.Helper()
	request, err := http.NewRequest(
		scenario.method,
		f.baseURL+scenario.path,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range scenario.headers {
		request.Header.Set(key, value)
	}
	request.Header.Set("HX-Current-URL", scenario.current)
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	location := response.Header.Get("HX-Redirect")
	if scenario.status != http.StatusSeeOther && location == "" {
		t.Fatal("HTMX response did not request a full login navigation")
	}
	if location == "" {
		location = response.Header.Get("Location")
	}
	if response.StatusCode != scenario.status {
		t.Fatalf("status = %d", response.StatusCode)
	}
	parsed, err := url.Parse(location)
	if err != nil || parsed.Path != "/login" || parsed.Host != "" {
		t.Fatalf("redirect = %q", location)
	}
	assertLoginContextNext(t, scenario.name, parsed.Query().Get("next"))
}

func assertLoginContextNext(t *testing.T, scenario, next string) {
	t.Helper()
	want := map[string]string{
		"boosted": "/projects", "history": "/environments",
		"fragment": "/projects?name=context", "expired-write": "/projects/1",
	}[scenario]
	if next != want {
		t.Fatalf("next = %q, want %q", next, want)
	}
}

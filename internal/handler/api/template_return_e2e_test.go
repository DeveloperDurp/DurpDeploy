//go:build e2e

package api_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestProjectTemplateReturnAPIWebE2E(t *testing.T) {
	f := newProjectTemplateE2E(t)
	projectPath := fmt.Sprintf("/projects/%d", f.project.ID)
	for _, scenario := range []struct{ name, returnTo, destination string }{
		{"project", "", projectPath},
		{"templates", "/templates?type=projects", "/templates?type=projects"},
		{"external", "https://example.test/", projectPath},
		{"protocol-relative", "//example.test/", projectPath},
		{"other-project", "/projects/999999", projectPath},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f.checkTemplateSaveReturn(
				t,
				scenario.name,
				scenario.returnTo,
				scenario.destination,
			)
		})
	}
}

func (f projectTemplateE2E) checkTemplateSaveReturn(
	t *testing.T,
	name, returnTo, destination string,
) {
	t.Helper()
	path := fmt.Sprintf("/projects/%d/project-templates", f.project.ID)
	form := url.Values{"return_to": {returnTo}}
	page := f.request(
		t,
		"GET",
		path+"/new?"+form.Encode(),
		"",
		"",
		f.session,
		false,
		200,
	)
	marker := fmt.Sprintf(`name="return_to" value="%s"`, destination)
	if !strings.Contains(string(page), marker) {
		t.Fatal("save form did not retain its allowed destination")
	}
	page = f.request(t, "POST", path, form.Encode(), "", f.session, false, 422)
	if !strings.Contains(string(page), marker) {
		t.Fatal("validation error lost the allowed return destination")
	}
	form.Set("name", "Return test "+name)
	req, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		f.server.URL+path,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", "member-role-csrf")
	client := *f.server.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther ||
		response.Header.Get("Location") != destination {
		t.Fatalf(
			"save returned %d Location %q, want 303 %q",
			response.StatusCode,
			response.Header.Get("Location"),
			destination,
		)
	}
}

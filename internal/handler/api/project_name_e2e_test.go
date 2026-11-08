//go:build e2e

package api_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

func TestProjectNamesAPIWebE2E(t *testing.T) {
	f := newProjectTemplateE2E(t)
	path := fmt.Sprintf("/projects/%d", f.project.ID)
	before := string(f.jsonRequest(t, "GET", "/api/v1"+path, nil, 200))
	count, err := f.h.repo.Queries.CountProjects(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		strings.Repeat("a", 256), strings.Repeat("é", 256),
		strings.Repeat("😀", 128), strings.Repeat("a", 100*1024),
	} {
		input := map[string]string{"name": name, "description": "rejected"}
		for _, target := range []struct{ method, path string }{
			{"POST", "/api/v1/projects"}, {"PUT", "/api/v1" + path},
		} {
			body := f.jsonRequest(t, target.method, target.path, input, 422)
			checkProjectNameError(t, string(body))
		}
		for _, target := range []struct{ method, path string }{
			{"POST", "/projects"}, {"PUT", path},
		} {
			body := f.request(t, target.method, target.path,
				url.Values{"name": {name}, "description": {"draft"}}.Encode(),
				"", f.session, false, 422)
			checkProjectNameError(t, string(body))
			if !strings.Contains(string(body), "draft") {
				t.Fatal("validation discarded description")
			}
		}
		f.rejectProjectTemplateName(t, name)
	}
	if string(f.jsonRequest(t, "GET", "/api/v1"+path, nil, 200)) != before {
		t.Fatal("invalid update changed project")
	}
	if after, err := f.h.repo.Queries.CountProjects(t.Context()); err != nil ||
		after != count {
		t.Fatalf(
			"invalid creates changed project count: %d -> %d: %v",
			count,
			after,
			err,
		)
	}
	f.checkAcceptedProjectNames(t)
	f.jsonRequest(
		t,
		"POST",
		"/api/v1/projects",
		map[string]string{"name": " "},
		400,
	)
	f.request(t, "POST", "/projects", "name=", "", f.session, false, 422)
}

func (f projectTemplateE2E) rejectProjectTemplateName(
	t *testing.T,
	name string,
) {
	t.Helper()
	input := f.input
	input.Name = name
	checkProjectNameError(t, string(f.jsonRequest(t, "POST",
		f.path+"/projects", input, 422)))
	form := url.Values{
		"name": {name}, "description": {"template draft"},
		"reviewed": {"true"}, "lifecycle_id": {"0"},
	}
	for _, variable := range f.snapshot.Variables {
		form.Set("variable_"+variable.Key, "fresh value")
		if variable.EnvironmentID != 0 {
			form.Set(
				"environment_"+strconv.FormatInt(variable.EnvironmentID, 10),
				strconv.FormatInt(f.environment.ID, 10),
			)
		}
	}
	checkProjectNameError(t, string(f.request(t, "POST",
		strings.TrimPrefix(f.path, "/api/v1")+"/projects",
		form.Encode(), "", f.session, false, 422)))
}

func (f projectTemplateE2E) checkAcceptedProjectNames(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		strings.Repeat("a", 255), strings.Repeat("é", 255),
		strings.Repeat("😀", 127) + "a",
	} {
		var project struct {
			ID                int64
			Name, Description string
		}
		input := map[string]string{
			"name": " " + name + " ", "description": strings.Repeat("d", 1024),
		}
		decodeStepLogTest(t,
			f.jsonRequest(t, "POST", "/api/v1/projects", input, 201), &project)
		if project.Name != name || project.Description != input["description"] {
			t.Fatal("create changed name or description")
		}
		createdPath := fmt.Sprintf("/projects/%d", project.ID)
		f.jsonRequest(t, "PUT", "/api/v1"+createdPath, input, 200)
		f.request(t, "PUT", createdPath,
			url.Values{"name": {" " + name + " "}}.Encode(),
			"", f.session, false, 200)
	}
}

func checkProjectNameError(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, "255 UTF-16 code units") {
		t.Fatalf("missing project length error: %.300s", body)
	}
}

func TestAuthenticatedReferrerPolicyE2E(t *testing.T) {
	f := newStaleProjectSaveFixture(t)
	for _, path := range []string{
		"/", "/projects", "/projects/new", "/settings/tokens", "/settings/security",
	} {
		req, err := http.NewRequestWithContext(
			t.Context(),
			"GET",
			f.server.URL+path,
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
		response, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != 200 ||
			response.Header.Get("Referrer-Policy") != "no-referrer" ||
			response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf(
				"%s: status=%d headers=%v",
				path,
				response.StatusCode,
				response.Header,
			)
		}
	}
}

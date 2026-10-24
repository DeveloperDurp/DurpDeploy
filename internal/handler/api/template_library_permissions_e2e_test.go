//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/repository"
)

func TestTemplateLibraryPermissionsAPIWebE2E(t *testing.T) {
	for _, scope := range []string{"project", "global"} {
		for _, role := range []string{"viewer", "deployer"} {
			t.Run(scope+"/"+role, func(t *testing.T) {
				checkLibraryPermissionDenied(t, scope, role)
			})
		}
	}
}

func checkLibraryPermissionDenied(t *testing.T, scope, role string) {
	t.Helper()
	f := newLibraryE2E(t)
	entry := f.preview(t, "http-health-check")
	f.changeRole(t, scope, role)
	f.json(t, "GET", f.path, nil, 200)
	f.json(t, "GET", f.path+"/http-health-check", nil, 200)
	f.json(
		t,
		"POST",
		f.path+"/http-health-check/apply",
		libraryInput(entry),
		403,
	)
	page := f.request(
		t,
		"GET",
		strings.TrimPrefix(f.path, "/api/v1")+"/http-health-check",
		"",
		"",
		f.session,
		false,
		200,
	)
	if strings.Contains(
		string(page),
		"data-apply-library-template",
	) ||
		!strings.Contains(string(page), "data-library-readonly") {
		t.Fatal("restricted user sees an actionable template form")
	}
	want := 403
	hx := role == "viewer" && scope == "global"
	if hx {
		want = 200
	}
	f.request(
		t,
		"POST",
		strings.TrimPrefix(
			f.path,
			"/api/v1",
		)+"/http-health-check/apply",
		libraryForm(entry).Encode(),
		"",
		f.session,
		hx,
		want,
	)
	f.request(
		t,
		"POST",
		strings.TrimPrefix(
			f.path,
			"/api/v1",
		)+"/http-health-check/apply",
		libraryForm(entry).Encode(),
		"",
		f.session,
		true,
		200,
	)
	var audit struct{ Total int }
	decodeStepLogTest(
		t,
		f.request(
			t,
			"GET",
			"/api/v1/admin/audit?action=apply_library_template",
			"",
			f.adminToken,
			"",
			false,
			200,
		),
		&audit,
	)
	if audit.Total != 0 {
		t.Fatal(
			"a rejected application was audited as a successful change",
		)
	}
	steps, err := f.h.repo.Queries.ListStepsByProject(
		t.Context(),
		f.project.ID,
	)
	if err != nil || len(steps) != 0 {
		t.Fatal("permission failure changed project steps", err)
	}
}

func TestTemplateLibraryScopedTokenAPIWebE2E(t *testing.T) {
	f := newLibraryE2E(t)
	entry := f.preview(t, "http-health-check")
	row, token := seedAPIToken(t, f.h.repo, f.user.ID)
	_, err := f.h.repo.ReplaceTokenScope(
		t.Context(),
		row,
		repository.TokenScope{
			Scope: "scoped", Role: "maintainer", ProjectIDs: []int64{f.project.ID},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.request(t, "GET", "/api/v1/template-library", "", token, "", false, 403)
	f.request(t, "GET", f.path, "", token, "", false, 200)
	f.request(t, "GET", f.path+"/http-health-check", "", token, "", false, 200)
	foreign := seedProject(t, f.h.repo)
	foreignPath := fmt.Sprintf(
		"/api/v1/projects/%d/template-library",
		foreign.ID,
	)
	f.request(t, "GET", foreignPath, "", token, "", false, 403)
	f.request(t, "GET", foreignPath, "", f.token, "", false, 403)
	input, err := json.Marshal(libraryInput(entry))
	if err != nil {
		t.Fatal(err)
	}
	f.request(
		t,
		"POST",
		foreignPath+"/http-health-check/apply",
		string(input),
		token,
		"",
		false,
		403,
	)
	for _, role := range []string{"viewer", "deployer", "maintainer"} {
		_, err := f.h.repo.ReplaceTokenScope(
			t.Context(),
			row,
			repository.TokenScope{
				Scope: "scoped", Role: role, ProjectIDs: []int64{f.project.ID},
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		status := 403
		if role == "maintainer" {
			status = 201
		}
		f.request(
			t,
			"POST",
			f.path+"/http-health-check/apply",
			string(input),
			token,
			"",
			false,
			status,
		)
	}
	f.request(t, "GET", "/api/v1/template-library", "", "", "", false, 401)
}

func TestTemplateLibraryCSRFAPIWebE2E(t *testing.T) {
	f := newLibraryE2E(t)
	entry := f.preview(t, "http-health-check")
	path := strings.TrimPrefix(f.path, "/api/v1") + "/http-health-check/apply"
	req, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		f.server.URL+path,
		strings.NewReader(libraryForm(entry).Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: f.session})
	response, err := f.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatalf("write without CSRF returned %d", response.StatusCode)
	}
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatal("CSRF rejection changed project steps", err)
	}
}

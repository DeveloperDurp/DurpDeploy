//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

const sharedTemplateOriginal = "# shared-original-literal\nprintf '%s' \"$SHARED_REFERENCE\"\n"

const sharedTemplateCurrent = "# shared-current-literal\nprintf '%s' \"$SHARED_REFERENCE\"\n"

type sharedTemplateFixture struct {
	staleProjectSaveFixture
	template db.StepTemplate
	path     string
}

func newSharedTemplateFixture(t *testing.T) sharedTemplateFixture {
	t.Helper()
	f := sharedTemplateFixture{
		staleProjectSaveFixture: newLibraryE2E(t).staleProjectSaveFixture,
	}
	input := `{"name":"Shared boundary step","script_body":"# shared-original-literal\nprintf '%s' \"$SHARED_REFERENCE\"\n","execution_target":"agent","agent_execution_mode":"container","container_image":"example/shared:reviewed","agent_selectors":["library-host"],"variable_names":["SHARED_REFERENCE"]}`
	var step struct{ ID int64 }
	decodeStepLogTest(t, f.request(t, "POST",
		fmt.Sprintf("/api/v1/projects/%d/steps", f.project.ID),
		input, f.token, "", false, 201), &step)
	f.request(t, "POST", fmt.Sprintf(
		"/projects/%d/steps/%d/save-as-template", f.project.ID, step.ID,
	), "", "", f.session, true, 200)
	templates, err := f.h.repo.Queries.ListStepTemplates(t.Context())
	if err != nil || len(templates) != 1 {
		t.Fatalf("saved templates: %v, %v", templates, err)
	}
	f.template = templates[0]
	if f.template.ScriptBody != sharedTemplateOriginal {
		t.Fatal("saving a step changed its full script")
	}
	f.path = fmt.Sprintf("/api/v1/templates/%d", f.template.ID)
	input = strings.Replace(
		input,
		"shared-original-literal",
		"shared-current-literal",
		1,
	)
	f.request(t, "PUT", f.path, input, f.token, "", false, 200)
	return f
}

func TestSharedTemplateVisibilityAPIWebE2E(t *testing.T) {
	f := newSharedTemplateFixture(t)
	for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
		t.Run(role, func(t *testing.T) {
			destination := seedProject(t, f.h.repo)
			f.checkReader(t, role, destination.ID)
		})
	}
	// Removing source membership does not unpublish a previously shared copy.
	f.request(t, "DELETE", fmt.Sprintf(
		"/api/v1/projects/%d/members/%d", f.project.ID, f.user.ID,
	), "", f.adminToken, "", false, 204)
	f.request(t, "GET", fmt.Sprintf("/api/v1/projects/%d", f.project.ID),
		"", f.token, "", false, 403)
	assertSharedText(t, f.request(t, "GET", f.path,
		"", f.token, "", false, 200), "shared-current-literal")
	assertSharedText(t, f.request(t, "GET", f.path+"/history",
		"", f.token, "", false, 200), "shared-original-literal")
	for _, path := range []string{"/api/v1/templates", f.path, f.path + "/history"} {
		f.request(t, "GET", path, "", "", "", false, 401)
	}
	for _, path := range []string{"/templates", fmt.Sprintf("/templates/%d/history", f.template.ID)} {
		page := f.request(t, "GET", path, "", "", "", false, 200)
		if strings.Contains(string(page), "shared-current-literal") {
			t.Fatal("anonymous browser read a shared script")
		}
	}
}

func (f sharedTemplateFixture) checkReader(
	t *testing.T,
	role string,
	destination int64,
) {
	t.Helper()
	reader := seedAPIUser(t, f.h.repo, role+"@shared-template.test", role)
	row, token := seedAPIToken(t, f.h.repo, reader.ID)
	if _, err := f.h.repo.ReplaceTokenScope(t.Context(), row, repository.TokenScope{
		Scope: "global", Role: role,
	}); err != nil {
		t.Fatal(err)
	}
	session := memberRoleSession(t, f.h, reader.ID)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: destination, UserID: reader.ID, Role: "maintainer",
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/templates", f.path} {
		assertSharedText(
			t,
			f.request(t, "GET", path, "", token, "", false, 200),
			"shared-current-literal",
			"example/shared:reviewed",
			"library-host",
			"SHARED_REFERENCE",
		)
	}
	if sharedTemplateScript(t, f.request(t, "GET", f.path,
		"", token, "", false, 200)) != sharedTemplateCurrent {
		t.Fatal("detail response changed the full script")
	}
	if role != "admin" {
		f.request(t, "GET", fmt.Sprintf("/api/v1/projects/%d", f.project.ID),
			"", token, "", false, 403)
	}
	assertSharedText(
		t,
		f.request(t, "GET", f.path+"/history", "", token, "", false, 200),
		"shared-original-literal",
		"shared-current-literal",
		"library-host",
	)
	for _, path := range []string{"/templates", fmt.Sprintf("/templates/%d/history", f.template.ID)} {
		assertSharedText(
			t,
			f.request(t, "GET", path, "", "", session, false, 200),
			"shared-current-literal",
			"SHARED_REFERENCE",
		)
	}
	assertSharedText(
		t,
		f.request(t, "GET", fmt.Sprintf("/templates/%d/history", f.template.ID),
			"", "", session, false, 200),
		"shared-original-literal",
	)
	picker := fmt.Sprintf("/projects/%d/templates-picker", destination)
	assertSharedText(t, f.request(t, "GET", "/api/v1"+picker,
		"", token, "", false, 200), "shared-current-literal", "library-host")
	assertSharedText(t, f.request(t, "GET", picker,
		"", "", session, false, 200), "Shared boundary step")
	f.checkCopy(t, role, destination, session)
	if _, err := f.h.repo.ReplaceTokenScope(t.Context(), row, repository.TokenScope{
		Scope: "scoped", Role: "viewer", ProjectIDs: []int64{destination},
	}); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/templates", f.path, f.path + "/history", "/api/v1" + picker} {
		f.request(t, "GET", path, "", token, "", false, 403)
	}
	// Curated definitions remain available within the selected project.
	f.request(t, "GET", fmt.Sprintf(
		"/api/v1/projects/%d/template-library/http-health-check", destination,
	), "", token, "", false, 200)
}

func (f sharedTemplateFixture) checkCopy(
	t *testing.T, role string, destination int64, session string,
) {
	t.Helper()
	copyPath := fmt.Sprintf(
		"/projects/%d/steps/from-template/%d",
		destination,
		f.template.ID,
	)
	status := 200
	if role == "deployer" {
		status = 403
	}
	// Viewer HTMX writes return a toast without changing the destination.
	f.request(t, "POST", copyPath, "", "", session, role == "viewer", status)
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), destination)
	if err != nil {
		t.Fatal(err)
	}
	want := role == "maintainer" || role == "admin"
	found := false
	for _, step := range steps {
		if step.ScriptBody == sharedTemplateCurrent {
			found = true
		}
	}
	if found != want {
		t.Fatalf("copy authoring boundary: found=%v, want=%v", found, want)
	}
}

func assertSharedText(t *testing.T, body []byte, markers ...string) {
	t.Helper()
	for _, marker := range markers {
		if !strings.Contains(string(body), marker) {
			t.Fatalf("response omitted shared definition marker %q", marker)
		}
	}
}

// Keep the full script assertion independent of JSON string escaping.
func sharedTemplateScript(t *testing.T, body []byte) string {
	t.Helper()
	var response struct {
		ScriptBody string `json:"script_body"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatal(err)
	}
	return response.ScriptBody
}

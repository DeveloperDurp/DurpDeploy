package handler_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

// Codex PR #97 P1: web `/templates` create/edit must collect
// container_image and variable_names, validate at the boundary, and
// persist them; API-created template metadata must survive a web edit.

// TemplateFormCreateLocalRoundTrip checks the full create/update path
// on the web form: a brand-new local template carries its image and
// variable names into the DB, and a subsequent edit keeps them.
func TestTemplateWebCreateLocalRoundTripPersistsImageAndVariables(
	t *testing.T,
) {
	h := newProjectHarness(t)

	form := url.Values{
		"name":            {"local-template"},
		"script_body":     {"echo hi"},
		"interpreter":     {"bash"},
		"container_image": {"alpine:3.20"},
		"variable_names":  {"DEPLOY_ENV, API_KEY"},
		"csrf_token":      {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		h.server.URL+"/templates",
		form,
	)
	if err != nil {
		t.Fatalf("POST /templates: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"create status=%d, want 303 (snippet=%s)",
			response.StatusCode,
			bodySnippet(response),
		)
	}

	templates, err := h.repo.Queries.ListStepTemplates(t.Context())
	if err != nil {
		t.Fatalf("list templates: %v", err)
	}
	if len(templates) != 1 {
		t.Fatalf("expected 1 template, got %d", len(templates))
	}
	got := templates[0]
	if got.ContainerImage != "alpine:3.20" {
		t.Fatalf(
			"created container_image=%q, want alpine:3.20",
			got.ContainerImage,
		)
	}
	wantNames := []string{"DEPLOY_ENV", "API_KEY"}
	var names []string
	if err := json.Unmarshal([]byte(got.VariableNames), &names); err != nil {
		t.Fatalf(
			"decode variable_names: %v (raw=%q)",
			err,
			got.VariableNames,
		)
	}
	if fmt.Sprint(names) != fmt.Sprint(wantNames) {
		t.Fatalf(
			"created variable_names=%v, want %v",
			names, wantNames,
		)
	}

	// Edit the template via the web form; the existing image and
	// variables must round-trip into the DB without being wiped.
	updateForm := url.Values{
		"name":            {"local-template"},
		"script_body":     {"echo updated"},
		"interpreter":     {"bash"},
		"container_image": {"alpine:3.20"},
		"variable_names":  {"DEPLOY_ENV, API_KEY"},
		"csrf_token":      {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		fmt.Sprintf("%s/templates/%d", h.server.URL, got.ID),
		strings.NewReader(updateForm.Encode()),
	)
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-CSRF-Token", h.csrfToken())
	putResp, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatalf("PUT /templates/%d: %v", got.ID, err)
	}
	putResp.Body.Close()
	if putResp.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"update status=%d, want 303",
			putResp.StatusCode,
		)
	}

	updated, err := h.repo.Queries.GetStepTemplate(t.Context(), got.ID)
	if err != nil {
		t.Fatalf("get updated template: %v", err)
	}
	if updated.ContainerImage != "alpine:3.20" {
		t.Fatalf(
			"after update container_image=%q, want alpine:3.20",
			updated.ContainerImage,
		)
	}
	if err := json.Unmarshal(
		[]byte(updated.VariableNames),
		&names,
	); err != nil {
		t.Fatalf("decode variable_names: %v", err)
	}
	if fmt.Sprint(names) != fmt.Sprint(wantNames) {
		t.Fatalf(
			"after update variable_names=%v, want %v",
			names, wantNames,
		)
	}
}

// TestTemplateWebUpdatePreservesApiMetadata reproduces the P1 bug where
// a template created via the API (with a container image and variable
// names) had those fields wiped when the user later edited it through
// the web form. The web handler used to call UpdateStepTemplate without
// reading container_image / variable_names from the form, so the
// sqlc update wrote empty strings and lost the API-supplied metadata.
func TestTemplateWebUpdatePreservesApiMetadata(t *testing.T) {
	h := newProjectHarness(t)

	// Seed the template via the repository, the path the API uses.
	tpl, err := h.repo.CreateStepTemplateWithPlacement(
		t.Context(),
		db.CreateStepTemplateParams{
			Name:           "api-created",
			ScriptBody:     "echo hi",
			Interpreter:    "bash",
			ContainerImage: "python:3.12",
			VariableNames:  `["DEPLOY_ENV","API_KEY"]`,
		},
		"local",
		nil,
	)
	if err != nil {
		t.Fatalf("seed api-created template: %v", err)
	}

	// Web PUT: submit only name + script_body. The image and variable
	// names remain in the form (the page renders them as defaults), and
	// the handler now persists them instead of zeroing them out.
	form := url.Values{
		"name":            {"api-created"},
		"script_body":     {"echo edited"},
		"interpreter":     {"bash"},
		"container_image": {"python:3.12"},
		"variable_names":  {"DEPLOY_ENV, API_KEY"},
		"csrf_token":      {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		fmt.Sprintf("%s/templates/%d", h.server.URL, tpl.ID),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-CSRF-Token", h.csrfToken())
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatalf("PUT /templates/%d: %v", tpl.ID, err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"update status=%d, want 303",
			response.StatusCode,
		)
	}

	updated, err := h.repo.Queries.GetStepTemplate(t.Context(), tpl.ID)
	if err != nil {
		t.Fatalf("get updated template: %v", err)
	}
	if updated.ContainerImage != "python:3.12" {
		t.Fatalf(
			"api metadata wiped: container_image=%q, want python:3.12",
			updated.ContainerImage,
		)
	}
	var names []string
	if err := json.Unmarshal(
		[]byte(updated.VariableNames),
		&names,
	); err != nil {
		t.Fatalf(
			"decode variable_names: %v (raw=%q)",
			err,
			updated.VariableNames,
		)
	}
	wantNames := []string{"DEPLOY_ENV", "API_KEY"}
	if fmt.Sprint(names) != fmt.Sprint(wantNames) {
		t.Fatalf(
			"api metadata wiped: variable_names=%v, want %v",
			names, wantNames,
		)
	}
	if updated.ScriptBody != "echo edited" {
		t.Fatalf(
			"script_body=%q, want echo edited",
			updated.ScriptBody,
		)
	}

	versions, err := h.repo.Queries.ListStepTemplateVersions(
		t.Context(), tpl.ID,
	)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	if versions[0].ContainerImage != "python:3.12" {
		t.Fatalf(
			"latest version container_image=%q, want python:3.12",
			versions[0].ContainerImage,
		)
	}
}

// TestTemplateWebCreateRejectsMissingImage is the P1 boundary check:
// the web form must reject a local template without a container image
// with 422, matching the API contract.
func TestTemplateWebCreateRejectsMissingImage(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"name":            {"no-image"},
		"script_body":     {"echo hi"},
		"interpreter":     {"bash"},
		"container_image": {""},
		"variable_names":  {""},
		"csrf_token":      {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		h.server.URL+"/templates",
		form,
	)
	if err != nil {
		t.Fatalf("POST /templates: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status=%d, want 422 (snippet=%s)",
			response.StatusCode,
			bodySnippet(response),
		)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "Container image is required") {
		t.Fatalf(
			"422 body missing required-image message; got: %s",
			body,
		)
	}
	templates, err := h.repo.Queries.ListStepTemplates(t.Context())
	if err != nil || len(templates) != 0 {
		t.Fatalf("templates=%+v error=%v", templates, err)
	}
}

// TestReleaseListShowsLegacyWarningOnOmittedTarget is the P2 check:
// releaseStepLegacyHost must flag a step whose snapshot omitted the
// execution_target field. Those releases were snapshotted before the
// server-container requirement landed and the runner refuses to start
// them; the user must be told.
func TestReleaseListShowsLegacyWarningOnOmittedTarget(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("legacy-release-omitted")
	snapshot := []map[string]any{
		{
			"name":            "legacy",
			"script_body":     "hostname",
			"interpreter":     "bash",
			"container_image": "",
			"variable_names":  []string{},
			"sort_order":      1,
			"timeout_seconds": 0,
			"max_retries":     0,
			"agent_selectors": []string{},
		},
	}
	encoded, _ := json.Marshal(snapshot)
	h.makeReleaseRaw(
		t.Context(), project.ID, "1.0.0", string(encoded),
	)

	response, err := h.authedClient().Get(
		fmt.Sprintf(
			"%s/projects/%d/releases", h.server.URL, project.ID,
		),
	)
	if err != nil {
		t.Fatalf("GET releases: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", response.StatusCode)
	}
	body := readBody(t, response)
	if !strings.Contains(body, "data-release-needs-recreation") {
		t.Fatalf(
			"releases list missing legacy warning for omitted target; got snippet: %s",
			bodySnippet(response),
		)
	}

	// And the per-release detail page must show the same warning.
	detailResp, err := h.authedClient().Get(
		fmt.Sprintf(
			"%s/projects/%d/releases/%d",
			h.server.URL, project.ID, h.firstReleaseID(t, project.ID),
		),
	)
	if err != nil {
		t.Fatalf("GET release detail: %v", err)
	}
	defer detailResp.Body.Close()
	if detailResp.StatusCode != http.StatusOK {
		t.Fatalf("detail status=%d, want 200", detailResp.StatusCode)
	}
	detailBody := readBody(t, detailResp)
	if !strings.Contains(detailBody, "data-release-needs-recreation") {
		t.Fatalf(
			"release detail missing legacy warning for omitted target; got: %s",
			detailBody,
		)
	}
	if !strings.Contains(detailBody, "data-release-step-legacy") {
		t.Fatalf(
			"release detail missing per-step legacy badge for omitted target; got: %s",
			detailBody,
		)
	}
}

func (h *projectHarness) firstReleaseID(t *testing.T, projectID int64) int64 {
	t.Helper()
	releases, err := h.repo.Queries.ListReleasesByProject(
		t.Context(),
		projectID,
	)
	if err != nil {
		t.Fatalf("list releases: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("expected 1 release, got %d", len(releases))
	}
	return releases[0].ID
}

// TestTemplateWebUpdateAgentPreservesTargetAndNoImage is the
// follow-up regression: the first P1 fix always validated the
// container config against "local", so an edited agent template
// (target=agent, no image, agent selectors) was rejected with 422.
// The handler must validate against the template's actual target.
func TestTemplateWebUpdateAgentPreservesTargetAndNoImage(t *testing.T) {
	h := newProjectHarness(t)

	tpl, err := h.repo.CreateStepTemplateWithPlacement(
		t.Context(),
		db.CreateStepTemplateParams{
			Name:       "agent-tpl",
			ScriptBody: "uname -a",
		},
		"agent",
		[]string{"linux"},
	)
	if err != nil {
		t.Fatalf("seed agent template: %v", err)
	}

	// Web PUT: only name + script_body + interpreter. No
	// container_image / variable_names, since the agent form does
	// not render those inputs.
	form := url.Values{
		"name":        {"agent-tpl"},
		"script_body": {"uname -a -v"},
		"interpreter": {"bash"},
		"csrf_token":  {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		fmt.Sprintf("%s/templates/%d", h.server.URL, tpl.ID),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-CSRF-Token", h.csrfToken())
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatalf("PUT /templates/%d: %v", tpl.ID, err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"agent edit status=%d, want 303 (snippet=%s)",
			response.StatusCode,
			bodySnippet(response),
		)
	}

	updated, err := h.repo.Queries.GetStepTemplate(t.Context(), tpl.ID)
	if err != nil {
		t.Fatalf("get updated template: %v", err)
	}
	if updated.ExecutionTarget != "agent" {
		t.Fatalf(
			"agent target flipped: execution_target=%q, want agent",
			updated.ExecutionTarget,
		)
	}
	if updated.ContainerImage != "" {
		t.Fatalf(
			"agent image leaked: container_image=%q, want empty",
			updated.ContainerImage,
		)
	}
	if updated.ScriptBody != "uname -a -v" {
		t.Fatalf(
			"script_body=%q, want %q",
			updated.ScriptBody, "uname -a -v",
		)
	}

	selectors, err := h.repo.Queries.ListTemplateAgentSelectors(
		t.Context(), tpl.ID,
	)
	if err != nil {
		t.Fatalf("list agent selectors: %v", err)
	}
	if len(selectors) != 1 || selectors[0] != "linux" {
		t.Fatalf(
			"agent selectors lost: got %v, want [linux]",
			selectors,
		)
	}

	versions, err := h.repo.Queries.ListStepTemplateVersions(
		t.Context(), tpl.ID,
	)
	if err != nil {
		t.Fatalf("list versions: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("expected 2 versions, got %d", len(versions))
	}
	if versions[0].ExecutionTarget != "agent" {
		t.Fatalf(
			"newest version execution_target=%q, want agent",
			versions[0].ExecutionTarget,
		)
	}
}

// TestTemplateWebUpdateLocalRoundTripPreservesImage is the local-side
// regression for the agent-handling change: API-created local
// templates must round-trip their container_image and variable_names
// through the web edit, not get wiped by zero-valued update params.
func TestTemplateWebUpdateLocalRoundTripPreservesImage(t *testing.T) {
	h := newProjectHarness(t)

	tpl, err := h.repo.CreateStepTemplateWithPlacement(
		t.Context(),
		db.CreateStepTemplateParams{
			Name:           "local-tpl",
			ScriptBody:     "echo hi",
			ContainerImage: "alpine:3.20",
			VariableNames:  `["DEPLOY_ENV"]`,
		},
		"local",
		nil,
	)
	if err != nil {
		t.Fatalf("seed local template: %v", err)
	}

	form := url.Values{
		"name":            {"local-tpl"},
		"script_body":     {"echo edited"},
		"interpreter":     {"bash"},
		"container_image": {"alpine:3.20"},
		"variable_names":  {"DEPLOY_ENV"},
		"csrf_token":      {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		fmt.Sprintf("%s/templates/%d", h.server.URL, tpl.ID),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatalf("build PUT: %v", err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-CSRF-Token", h.csrfToken())
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatalf("PUT /templates/%d: %v", tpl.ID, err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"local edit status=%d, want 303 (snippet=%s)",
			response.StatusCode,
			bodySnippet(response),
		)
	}

	updated, err := h.repo.Queries.GetStepTemplate(t.Context(), tpl.ID)
	if err != nil {
		t.Fatalf("get updated template: %v", err)
	}
	if updated.ExecutionTarget != "local" {
		t.Fatalf(
			"local target flipped: execution_target=%q, want local",
			updated.ExecutionTarget,
		)
	}
	if updated.ContainerImage != "alpine:3.20" {
		t.Fatalf(
			"local image wiped: container_image=%q, want alpine:3.20",
			updated.ContainerImage,
		)
	}
	var names []string
	if err := json.Unmarshal(
		[]byte(updated.VariableNames),
		&names,
	); err != nil {
		t.Fatalf("decode variable_names: %v", err)
	}
	wantNames := []string{"DEPLOY_ENV"}
	if fmt.Sprint(names) != fmt.Sprint(wantNames) {
		t.Fatalf(
			"local variable_names=%v, want %v",
			names, wantNames,
		)
	}
	if updated.ScriptBody != "echo edited" {
		t.Fatalf(
			"script_body=%q, want echo edited",
			updated.ScriptBody,
		)
	}
}

//go:build e2e

package api_test

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/interpreter"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

type libraryE2E struct {
	staleProjectSaveFixture
	path string
}

func newLibraryE2E(t *testing.T) libraryE2E {
	t.Helper()
	f := libraryE2E{staleProjectSaveFixture: newStaleProjectSaveFixture(t)}
	f.path = fmt.Sprintf("/api/v1/projects/%d/template-library", f.project.ID)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	f.h.repo.SetSecretBox(box)
	agent, err := f.h.repo.Queries.CreateAgent(
		t.Context(),
		db.CreateAgentParams{
			ID: "library-agent", Name: "Library host", Endpoint: "https://agent.example.test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.AddAgentLabel(t.Context(), db.AddAgentLabelParams{
		AgentID: agent.ID, Label: "library-host",
	}); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f libraryE2E) json(
	t *testing.T,
	method, path string,
	input any,
	status int,
) []byte {
	t.Helper()
	body := ""
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		body = string(encoded)
	}
	return f.request(t, method, path, body, f.token, "", false, status)
}

func libraryInput(
	entry repository.LibraryTemplate,
) repository.ApplyLibraryTemplate {
	values := map[string]string{
		"ARTIFACT_URL":       "https://artifacts.example.test/app",
		"ARTIFACT_TOKEN":     "library-fresh-secret",
		"FILE_SOURCE":        "",
		"INSTALL_PATH":       "/opt/library/app",
		"SERVICE_NAME":       "library.service",
		"HEALTH_URL":         "http://127.0.0.1:8080/health",
		"COMPOSE_FILE":       "/opt/library/compose.yml",
		"COMPOSE_PROJECT":    "library",
		"APP_IMAGE":          "example/library:1.0",
		"TF_CONFIG_DIR":      "/opt/library/terraform",
		"TF_CONTAINER_IMAGE": "example/terraform-toolchain:reviewed",
		"TF_HTTP_USERNAME":   "fixture",
		"TF_HTTP_PASSWORD":   "library-fresh-secret",
	}
	input := repository.ApplyLibraryTemplate{
		Version:  entry.Version,
		Reviewed: true,
		AgentSelectors: []string{
			"library-host",
		},
		VariableValues: map[string]string{},
	}
	for key := range entry.Configuration {
		input.VariableValues[key] = values[key]
	}
	if !repository.LibraryUsesAgents(entry) {
		input.AgentSelectors = nil
	}
	return input
}

func TestTemplateLibraryAPIWebE2E(t *testing.T) {
	for _, template := range repository.TemplateLibrary() {
		slug := template.Slug
		t.Run(slug, func(t *testing.T) {
			f := newLibraryE2E(t)
			entry := f.preview(t, slug)
			f.checkLibraryCopy(t, entry)
		})
	}
}

func (f libraryE2E) preview(
	t *testing.T,
	slug string,
) repository.LibraryTemplate {
	t.Helper()
	var list struct {
		Items []repository.LibraryTemplate
		Total int
	}
	decodeStepLogTest(
		t,
		f.json(t, "GET", "/api/v1/template-library", nil, 200),
		&list,
	)
	if list.Total != 19 || len(list.Items) != 19 {
		t.Fatal(
			"library must contain three language variants and a Terraform workflow",
		)
	}
	var entry repository.LibraryTemplate
	decodeStepLogTest(t, f.json(t, "GET", f.path+"/"+slug, nil, 200), &entry)
	if entry.Slug != slug || entry.Version == "" ||
		len(entry.Dependencies) == 0 ||
		len(entry.Permissions) == 0 {
		t.Fatal("preview omitted library metadata")
	}
	page := string(
		f.request(t, "GET", strings.TrimPrefix(f.path, "/api/v1")+"/"+slug,
			"", "", f.session, false, 200),
	)
	for _, step := range entry.Snapshot.Steps {
		_, err := interpreter.Validate(step.Interpreter)
		if err != nil ||
			(step.ExecutionTarget != "agent" && step.ExecutionTarget != "local") ||
			step.AgentExecutionMode != "host" {
			t.Fatal("preview omitted supported execution placement")
		}
		if !strings.Contains(page, step.Name) {
			t.Fatal("web preview omitted an ordered step")
		}
	}
	for _, v := range entry.Snapshot.Variables {
		if !strings.Contains(page, v.Name) ||
			entry.Configuration[v.Key].Description == "" {
			t.Fatal("preview omitted required configuration")
		}
	}
	return entry
}

func (f libraryE2E) checkLibraryCopy(
	t *testing.T,
	entry repository.LibraryTemplate,
) {
	t.Helper()
	projectPath := fmt.Sprintf("/api/v1/projects/%d", f.project.ID)
	f.json(t, "POST", projectPath+"/steps", map[string]any{
		"name": "Existing", "script_body": "echo existing", "sort_order": 9,
		"execution_target": "agent", "agent_selectors": []string{"library-host"},
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.json(t, "POST", projectPath+"/releases",
		map[string]string{"version": "before-library"}, 201), &release)
	frozen, err := f.h.repo.Queries.GetRelease(t.Context(), release.ID)
	if err != nil {
		t.Fatal(err)
	}
	input := libraryInput(entry)
	var result repository.LibraryApplyResult
	decodeStepLogTest(
		t,
		f.json(t, "POST", f.path+"/"+entry.Slug+"/apply", input, 201),
		&result,
	)
	if result.StepsCreated != len(entry.Snapshot.Steps) ||
		result.VariablesCreated != len(entry.Snapshot.Variables) {
		t.Fatal("apply response did not identify copied configuration")
	}
	var steps struct {
		Items []struct {
			ID                   int64
			ScriptBody           string `json:"script_body"`
			SortOrder            int64  `json:"sort_order"`
			Interpreter          string `json:"interpreter"`
			ApprovalArtifactPath string `json:"approval_artifact_path"`
			ApprovalReviewPath   string `json:"approval_review_path"`
			ApprovalReviewFormat string `json:"approval_review_format"`
		}
	}
	decodeStepLogTest(
		t,
		f.json(t, "GET", projectPath+"/steps", nil, 200),
		&steps,
	)
	if len(steps.Items) != len(entry.Snapshot.Steps)+1 ||
		steps.Items[0].ScriptBody != "echo existing" {
		t.Fatal("library application replaced existing steps")
	}
	for i, step := range steps.Items[1:] {
		if step.SortOrder != int64(10+i) ||
			step.ScriptBody != entry.Snapshot.Steps[i].ScriptBody ||
			step.Interpreter != entry.Snapshot.Steps[i].Interpreter ||
			step.ApprovalArtifactPath != entry.Snapshot.Steps[i].ApprovalArtifactPath ||
			step.ApprovalReviewPath != entry.Snapshot.Steps[i].ApprovalReviewPath ||
			step.ApprovalReviewFormat != entry.Snapshot.Steps[i].ApprovalReviewFormat {
			t.Fatal("library steps did not append in workflow order")
		}
	}
	f.checkLibraryFrozenRelease(t, frozen)
	var deployments struct{ Total int }
	decodeStepLogTest(
		t,
		f.json(t, "GET", projectPath+"/deployments", nil, 200),
		&deployments,
	)
	if deployments.Total != 0 {
		t.Fatal("applying a template started a deployment")
	}
	publicVariables := f.json(t, "GET", projectPath+"/variables", nil, 200)
	if strings.Contains(string(publicVariables), "library-fresh-secret") {
		t.Fatal("public variable API exposed a supplied secret")
	}
	f.checkLibraryAudit(t, entry)
	f.json(t, "POST", f.path+"/"+entry.Slug+"/apply", input, 409)
	// Editing the copy must not edit the bundled source or the frozen release.
	f.json(
		t,
		"PUT",
		fmt.Sprintf("%s/steps/%d", projectPath, steps.Items[1].ID),
		map[string]any{
			"name":             "Customized copy",
			"script_body":      "echo customized",
			"execution_target": "agent",
			"agent_selectors":  []string{"library-host"},
			"sort_order":       10,
		},
		200,
	)
	var preview repository.LibraryTemplate
	decodeStepLogTest(
		t,
		f.json(t, "GET", f.path+"/"+entry.Slug, nil, 200),
		&preview,
	)
	if !reflect.DeepEqual(preview, entry) {
		t.Fatal("editing a copy changed the library")
	}
	f.checkLibraryFrozenRelease(t, frozen)
}

func (f libraryE2E) checkLibraryFrozenRelease(t *testing.T, frozen db.Release) {
	t.Helper()
	current, err := f.h.repo.Queries.GetRelease(t.Context(), frozen.ID)
	if err != nil || !reflect.DeepEqual(current, frozen) {
		t.Fatal("changing project configuration changed a frozen release", err)
	}
}

func TestTemplateLibraryLifecycleConflictAPIWebE2E(t *testing.T) {
	f := newLibraryE2E(t)
	entry := f.preview(t, "http-health-check")
	lifecycle, err := f.h.repo.Queries.CreateLifecycle(
		t.Context(),
		db.CreateLifecycleParams{Name: "Library conflict"},
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.SetProjectLifecycle(t.Context(), db.SetProjectLifecycleParams{
		ID: f.project.ID, LifecycleID: sql.NullInt64{Int64: lifecycle.ID, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateLifecycleVariable(t.Context(), db.CreateLifecycleVariableParams{
		LifecycleID: lifecycle.ID, Name: "HEALTH_URL", Value: sql.NullString{String: "http://keep.example.test", Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	f.json(
		t,
		"POST",
		f.path+"/http-health-check/apply",
		libraryInput(entry),
		409,
	)
	variables, err := f.h.repo.Queries.ListVariablesByProject(
		t.Context(),
		f.project.ID,
	)
	if err != nil || len(variables) != 0 {
		t.Fatal("library application shadowed inherited configuration", err)
	}
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatal("inherited conflict left partial steps", err)
	}
}

func (f libraryE2E) checkLibraryAudit(
	t *testing.T,
	entry repository.LibraryTemplate,
) {
	t.Helper()
	body := f.request(
		t,
		"GET",
		"/api/v1/admin/audit?limit=100",
		"",
		f.adminToken,
		"",
		false,
		200,
	)
	if strings.Contains(string(body), "library-fresh-secret") {
		t.Fatal("audit exposed a supplied secret")
	}
	var entries struct {
		Items []struct {
			Action   string
			EntityID struct{ Int64 int64 } `json:"entity_id"`
			Details  struct {
				String string
			}
		}
	}
	decodeStepLogTest(t, body, &entries)
	for _, row := range entries.Items {
		if row.Action == "apply_library_template" &&
			row.EntityID.Int64 == f.project.ID &&
			strings.Contains(row.Details.String, entry.Slug) {
			return
		}
	}
	t.Fatal("application did not audit its destination and template version")
}

func TestTemplateLibraryTerraformValidationAPIWebE2E(t *testing.T) {
	f := newLibraryE2E(t)
	entry := f.preview(t, "terraform-plan-apply")
	for _, edit := range []func(*repository.ApplyLibraryTemplate){
		func(input *repository.ApplyLibraryTemplate) { delete(input.VariableValues, "TF_CONTAINER_IMAGE") },
		func(input *repository.ApplyLibraryTemplate) {
			input.VariableValues["TF_CONTAINER_IMAGE"] = "--privileged"
		},
		func(input *repository.ApplyLibraryTemplate) { input.VariableValues["TF_CONFIG_DIR"] = "relative" },
		func(input *repository.ApplyLibraryTemplate) { input.AgentSelectors = []string{"library-host"} },
		func(input *repository.ApplyLibraryTemplate) {
			input.VariableValues["TF_HTTP_PASSWORD"] = "invalid\nsecret"
		},
	} {
		input := libraryInput(entry)
		edit(&input)
		body := f.json(
			t,
			"POST",
			f.path+"/terraform-plan-apply/apply",
			input,
			422,
		)
		if strings.Contains(string(body), "invalid\nsecret") {
			t.Fatal("validation reflected a backend secret")
		}
	}
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatal("rejected input created steps", err)
	}
	variables, err := f.h.repo.Queries.ListVariablesByProject(
		t.Context(),
		f.project.ID,
	)
	if err != nil || len(variables) != 0 {
		t.Fatal("rejected input created variables", err)
	}
}

func TestTemplateLibraryValidationAPIWebE2E(t *testing.T) {
	f := newLibraryE2E(t)
	entry := f.preview(t, "linux-service")
	for _, change := range []struct {
		name   string
		status int
		edit   func(*repository.ApplyLibraryTemplate)
	}{
		{"unreviewed", 422, func(p *repository.ApplyLibraryTemplate) { p.Reviewed = false }},
		{"version", 409, func(p *repository.ApplyLibraryTemplate) { p.Version = "old" }},
		{"required", 422, func(p *repository.ApplyLibraryTemplate) { delete(p.VariableValues, "INSTALL_PATH") }},
		{"unknown", 422, func(p *repository.ApplyLibraryTemplate) { p.VariableValues["OTHER"] = "value" }},
		{"token", 422, func(p *repository.ApplyLibraryTemplate) { p.VariableValues["ARTIFACT_TOKEN"] = "invalid\nheader" }},
		{"url", 422, func(p *repository.ApplyLibraryTemplate) { p.VariableValues["ARTIFACT_URL"] = "file:///etc/passwd" }},
		{"path", 422, func(p *repository.ApplyLibraryTemplate) { p.VariableValues["INSTALL_PATH"] = "relative" }},
		{"service", 422, func(p *repository.ApplyLibraryTemplate) { p.VariableValues["SERVICE_NAME"] = "--help" }},
		{"selector", 422, func(p *repository.ApplyLibraryTemplate) { p.AgentSelectors = []string{"unknown"} }},
		{"empty selectors", 422, func(p *repository.ApplyLibraryTemplate) { p.AgentSelectors = []string{" "} }},
	} {
		t.Run(change.name, func(t *testing.T) {
			input := libraryInput(entry)
			change.edit(&input)
			f.json(
				t,
				"POST",
				f.path+"/linux-service/apply",
				input,
				change.status,
			)
		})
	}
	f.json(t, "GET", f.path+"/missing", nil, 404)
	f.json(t, "GET", "/api/v1/template-library?offset=-1", nil, 400)
	f.json(
		t,
		"GET",
		"/api/v1/template-library?offset=9223372036854775807",
		nil,
		200,
	)
	form := libraryForm(entry)
	form.Set("variable_INSTALL_PATH", "")
	page := f.request(
		t,
		"POST",
		strings.TrimPrefix(f.path, "/api/v1")+"/linux-service/apply",
		form.Encode(),
		"",
		f.session,
		false,
		422,
	)
	if strings.Contains(string(page), "library-fresh-secret") ||
		!strings.Contains(string(page), "data-library-error") {
		t.Fatal("web validation lost feedback or exposed a fresh secret")
	}
	variables, err := f.h.repo.Queries.ListVariablesByProject(
		t.Context(),
		f.project.ID,
	)
	if err != nil || len(variables) != 0 {
		t.Fatal("rejected requests left partial variables", err)
	}
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) != 0 {
		t.Fatal("rejected requests left partial steps", err)
	}
}

func libraryForm(entry repository.LibraryTemplate) url.Values {
	input := libraryInput(entry)
	form := url.Values{
		"version":         {input.Version},
		"reviewed":        {"true"},
		"agent_selectors": {"library-host"},
	}
	for key, value := range input.VariableValues {
		form.Set("variable_"+key, value)
	}
	return form
}

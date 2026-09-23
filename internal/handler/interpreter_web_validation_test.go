package handler_test

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

// Gap 3: the web form endpoints must reject invalid interpreter values
// with 422, matching the repo contract for web POST validation failures.

func TestStepWebCreateRejectsInvalidInterpreter(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("invalid-interpreter")

	form := url.Values{
		"name":             {"bad"},
		"script_body":      {"echo nope"},
		"interpreter":      {"/bin/sh"},
		"sort_order":       {"1"},
		"execution_target": {"local"},
		"csrf_token":       {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
		form,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status=%d, want 422",
			response.StatusCode,
		)
	}
	steps, err := h.repo.Queries.ListStepsByProject(
		t.Context(), project.ID,
	)
	if err != nil || len(steps) != 0 {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
}

func TestStepWebUpdateRejectsInvalidInterpreter(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("invalid-interpreter-update")
	step, err := h.repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID: project.ID, Name: "good",
			ScriptBody: "echo hi", Interpreter: "python3",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"name":             {"good"},
		"script_body":      {"echo hi"},
		"interpreter":      {"ruby"},
		"sort_order":       {"1"},
		"execution_target": {"local"},
		"csrf_token":       {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		fmt.Sprintf(
			"%s/projects/%d/steps/%d",
			h.server.URL, project.ID, step.ID,
		),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-CSRF-Token", h.csrfToken())
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422", response.StatusCode)
	}
	updated, err := h.repo.Queries.GetStep(t.Context(), step.ID)
	if err != nil || updated.Interpreter != "python3" {
		t.Fatalf("step=%+v error=%v", updated, err)
	}
}

func TestTemplateWebCreateRejectsInvalidInterpreter(t *testing.T) {
	h := newProjectHarness(t)

	form := url.Values{
		"name":        {"bad"},
		"script_body": {"echo nope"},
		"interpreter": {"/bin/sh"},
		"csrf_token":  {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		h.server.URL+"/templates",
		form,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422", response.StatusCode)
	}
	templates, err := h.repo.Queries.ListStepTemplates(t.Context())
	if err != nil || len(templates) != 0 {
		t.Fatalf("templates=%+v error=%v", templates, err)
	}
}

func TestTemplateWebUpdateRejectsInvalidInterpreter(t *testing.T) {
	h := newProjectHarness(t)
	template, err := h.repo.CreateStepTemplateWithPlacement(
		t.Context(),
		db.CreateStepTemplateParams{
			Name: "good", ScriptBody: "echo hi", Interpreter: "python3",
		},
		"local",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"name":        {"good"},
		"script_body": {"echo hi"},
		"interpreter": {"ruby"},
		"csrf_token":  {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPut,
		fmt.Sprintf("%s/templates/%d", h.server.URL, template.ID),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-CSRF-Token", h.csrfToken())
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d, want 422", response.StatusCode)
	}
	updated, err := h.repo.Queries.GetStepTemplate(t.Context(), template.ID)
	if err != nil || updated.Interpreter != "python3" {
		t.Fatalf("template=%+v error=%v", updated, err)
	}
}

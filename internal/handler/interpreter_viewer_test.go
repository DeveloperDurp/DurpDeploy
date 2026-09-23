package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

// Gap 9: viewers must still SEE the interpreter on the steps page,
// and must not be able to change it (403 styled page for form posts,
// 200 + HX-Trigger toast for HTMX requests).

func TestViewerSeesInterpreterOnStepsPage(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("viewer-interpreter")
	if _, err := h.repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID: project.ID, Name: "py",
			ScriptBody: "print('ok')", Interpreter: "python3",
		},
	); err != nil {
		t.Fatal(err)
	}
	h.setRole("viewer")
	// Per-project authorization (P1-1): the viewer must be a member of
	// the project to read it. Member roles are constrained to
	// admin/deployer; the global viewer role still gates writes.
	if err := h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: project.ID,
			UserID:    h.sess.user.ID,
			Role:      "deployer",
		},
	); err != nil {
		t.Fatal(err)
	}

	response, err := h.authedClient().Get(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d, want 200", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "python3") {
		t.Fatal("steps page did not show interpreter python3 to viewer")
	}
}

func TestViewerCannotChangeInterpreterViaForm(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("viewer-interpreter")
	step, err := h.repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID: project.ID, Name: "py",
			ScriptBody: "print('ok')", Interpreter: "python3",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	h.setRole("viewer")
	if err := h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: project.ID,
			UserID:    h.sess.user.ID,
			Role:      "deployer",
		},
	); err != nil {
		t.Fatal(err)
	}

	form := url.Values{
		"name":             {"py"},
		"script_body":      {"print('ok')"},
		"interpreter":      {"pwsh"},
		"sort_order":       {"1"},
		"execution_target": {"local"},
		"csrf_token":       {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		context.Background(),
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
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("status=%d, want 403", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "Viewers cannot") {
		t.Fatalf("body missing viewer message: %s", body)
	}
	updated, err := h.repo.Queries.GetStep(t.Context(), step.ID)
	if err != nil || updated.Interpreter != "python3" {
		t.Fatalf("step=%+v error=%v", updated, err)
	}
}

func TestViewerHTMXInterpreterChangeReturnsToast(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("viewer-interpreter-htmx")
	step, err := h.repo.Queries.CreateStep(
		t.Context(),
		db.CreateStepParams{
			ProjectID: project.ID, Name: "py",
			ScriptBody: "print('ok')", Interpreter: "python3",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	h.setRole("viewer")

	form := url.Values{
		"name":             {"py"},
		"script_body":      {"print('ok')"},
		"interpreter":      {"pwsh"},
		"sort_order":       {"1"},
		"execution_target": {"local"},
		"csrf_token":       {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		context.Background(),
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
	request.Header.Set("HX-Request", "true")
	request.Header.Set("X-CSRF-Token", h.csrfToken())
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"status=%d, want 200 (HTMX path returns 200 + HX-Trigger)",
			response.StatusCode,
		)
	}
	trigger := response.Header.Get("HX-Trigger")
	if !strings.Contains(trigger, `"makeToast"`) ||
		!strings.Contains(trigger, "Viewers cannot") {
		t.Fatalf("HX-Trigger = %q", trigger)
	}
	updated, err := h.repo.Queries.GetStep(t.Context(), step.ID)
	if err != nil || updated.Interpreter != "python3" {
		t.Fatalf("step=%+v error=%v", updated, err)
	}
}

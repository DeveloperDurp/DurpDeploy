package handler_test

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestVariableAlpine_OwnsLifecycleOnSwappedFragment(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	project := h.makeProject("variable-alpine-fragment")
	request, err := http.NewRequest(
		http.MethodGet,
		fmt.Sprintf("%s/projects/%d/variables", h.server.URL, project.ID),
		nil,
	)
	if err != nil {
		t.Fatalf("create variables request: %v", err)
	}
	request.Header.Set("HX-Request", "true")

	// When
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatalf("get variables fragment: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read variables fragment: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	markup := string(body)
	if !strings.Contains(markup, `id="variables-content"`) ||
		!strings.Contains(markup, `x-data="variablesPage"`) ||
		!strings.Contains(markup, `x-on:click="override($event)"`) {
		t.Errorf("swapped variables fragment does not own Alpine lifecycle: %s", markup)
	}
}

func TestVariableAlpine_FullPageHasNoGlobalOverrideScript(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	project := h.makeProject("variable-alpine-page")

	// When
	response, err := h.authedClient().Get(
		fmt.Sprintf("%s/projects/%d/variables", h.server.URL, project.ID),
	)
	if err != nil {
		t.Fatalf("get variables page: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read variables page: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if strings.Contains(string(body), "document.addEventListener('click'") {
		t.Error("variables page retains a document-global override listener")
	}
}

func TestVariablesPreview_MasksSecretValueOnProjectDetail(t *testing.T) {
	// Given
	h := newProjectHarness(t)
	proj := h.makeProject("secret-preview")
	const plainValue = "visible-project-detail-value"
	const secretValue = "project-detail-secret-sentinel"
	makeVariableGlobal(t, h, proj.ID, "PLAIN_PREVIEW", plainValue, "")
	_, err := h.repo.CreateVariable(
		context.Background(),
		db.CreateVariableParams{
			ProjectID: proj.ID,
			Name:      "SECRET_PREVIEW",
			Value: sql.NullString{
				String: secretValue,
				Valid:  true,
			},
			Secret: 1,
		},
	)
	if err != nil {
		t.Fatalf("create secret preview variable: %v", err)
	}

	// When
	page := h.getProjectPage(proj.ID)

	// Then
	if strings.Contains(page, secretValue) {
		t.Errorf("project detail exposes secret preview value")
	}
	if !strings.Contains(page, plainValue) {
		t.Errorf("project detail omits non-secret preview value")
	}
	if !strings.Contains(page, "••••••••") {
		t.Errorf("project detail omits secret preview mask")
	}
}

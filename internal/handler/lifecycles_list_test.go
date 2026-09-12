package handler_test

import (
	"bytes"
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

func TestLifecycleListRow_renders_plain_name_and_merged_edit_action(t *testing.T) {
	// Given
	request, err := http.NewRequest(http.MethodGet, "/lifecycles", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request = auth.SetUser(request, &db.User{Role: "writer"})
	row := pages.LifecycleRow{
		Lifecycle:  db.Lifecycle{ID: 42, Name: "release flow", Description: sql.NullString{}},
		StageCount: 3,
	}
	var rendered bytes.Buffer

	// When
	err = pages.LifecycleListRow(row).Render(request.Context(), &rendered)
	if err != nil {
		t.Fatalf("render lifecycle row: %v", err)
	}
	body := rendered.String()

	// Then
	if !strings.Contains(body, `release flow`) {
		t.Errorf("lifecycle name is not plain text: %s", body)
	}
	if strings.Contains(body, `href="/lifecycles/42">release flow`) {
		t.Errorf("lifecycle name still links to detail: %s", body)
	}
	if !strings.Contains(body, `<a href="/lifecycles/42" class="btn btn-sm btn-ghost">Edit</a>`) {
		t.Errorf("writer Edit link missing: %s", body)
	}
	if strings.Contains(body, `/lifecycles/42/edit`) {
		t.Errorf("writer Edit link still opens separate page: %s", body)
	}
}

func TestLifecycleDetail_renders_settings_and_environment_assignment(t *testing.T) {
	// Given
	request, err := http.NewRequest(http.MethodGet, "/lifecycles/42", nil)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	request = auth.SetUser(request, &db.User{Role: "writer"})
	lifecycle := db.Lifecycle{ID: 42, Name: "release flow"}
	available := []db.Environment{{ID: 7, Name: "production"}}
	var rendered bytes.Buffer

	// When
	err = pages.LifecycleDetail(lifecycle, nil, available, "").
		Render(request.Context(), &rendered)
	if err != nil {
		t.Fatalf("render lifecycle detail: %v", err)
	}
	body := rendered.String()

	// Then
	for _, marker := range []string{
		`action="/lifecycles/42"`,
		`name="name"`,
		`name="description"`,
		`name="environment_id"`,
		`<option value="7">production</option>`,
	} {
		if !strings.Contains(body, marker) {
			t.Errorf("merged lifecycle workspace missing %q: %s", marker, body)
		}
	}
	promotionOrder := strings.Index(body, `>Promotion order</h2>`)
	lifecycleSettings := strings.Index(body, `>Lifecycle settings</h2>`)
	if promotionOrder < 0 || lifecycleSettings < 0 || promotionOrder > lifecycleSettings {
		t.Errorf("promotion order must appear before lifecycle settings: %s", body)
	}
}

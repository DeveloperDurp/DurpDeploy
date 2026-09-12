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

func TestLifecycleListRow_renders_plain_name_and_writer_edit_action(t *testing.T) {
	// Given
	request := auth.SetUser(
		http.NewRequest(http.MethodGet, "/lifecycles", nil),
		&db.User{Role: "writer"},
	)
	row := pages.LifecycleRow{
		Lifecycle:  db.Lifecycle{ID: 42, Name: "release flow", Description: sql.NullString{}},
		StageCount: 3,
	}
	var rendered bytes.Buffer

	// When
	err := pages.LifecycleListRow(row).Render(request.Context(), &rendered)
	if err != nil {
		t.Fatalf("render lifecycle row: %v", err)
	}
	body := rendered.String()

	// Then
	if !strings.Contains(body, `release flow`) {
		t.Errorf("lifecycle name is not plain text: %s", body)
	}
	if strings.Contains(body, `href="/lifecycles/42"`) {
		t.Errorf("lifecycle name still links to detail: %s", body)
	}
	if !strings.Contains(body, `<a href="/lifecycles/42/edit" class="btn btn-sm btn-ghost">Edit</a>`) {
		t.Errorf("writer Edit link missing: %s", body)
	}
	if strings.Count(body, `href="/lifecycles/42/edit"`) != 1 {
		t.Errorf("writer Edit link count = %d, want 1", strings.Count(body, `href="/lifecycles/42/edit"`))
	}
}

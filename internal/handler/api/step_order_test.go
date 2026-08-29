package api_test

import (
	"context"
	"net/http"
	"testing"
)

func TestStep_CreateNormalizesZeroSortOrder(t *testing.T) {
	h := newHarness(t)
	admin := h.seedUser(t, "admin@example.com", "admin")
	token := h.seedToken(t, admin)
	p := h.seedProject(t, admin)

	// Given
	rec := h.request(
		t,
		http.MethodPost,
		"/api/v1/projects/"+itoa(p.ID)+"/steps",
		token,
		`{"name":"deploy","script_body":"echo deploy","sort_order":0}`,
	)

	// When
	h.assertStatus(t, rec, http.StatusCreated)
	h.assertJSONField(t, rec, "sort_order", float64(1))

	steps, err := h.repo.Queries.ListStepsByProject(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("list steps: %v", err)
	}
	if len(steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(steps))
	}
	if got := steps[0].SortOrder; got != 1 {
		t.Fatalf("stored sort_order = %d", got)
	}

	// Then
	if got := steps[0].ProjectID; got != p.ID {
		t.Fatalf("project_id = %d", got)
	}
}

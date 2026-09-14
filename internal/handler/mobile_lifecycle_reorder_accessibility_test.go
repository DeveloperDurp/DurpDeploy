package handler_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

func renderLifecycleStageList(
	t *testing.T,
	stages []pages.LifecycleStageView,
) string {
	t.Helper()

	var rendered bytes.Buffer
	err := pages.LifecycleStageList(db.Lifecycle{ID: 1}, stages, nil).
		Render(context.Background(), &rendered)
	if err != nil {
		t.Fatalf("render lifecycle stages: %v", err)
	}

	return rendered.String()
}

func TestMobile_RenderedHTML_keeps_lifecycle_reorder_labels_unique_with_duplicate_or_empty_environment_names(
	t *testing.T,
) {
	// Given
	stages := []pages.LifecycleStageView{
		{
			Stage:       db.LifecycleStage{ID: 101},
			Environment: db.Environment{ID: 201, Name: "shared"},
		},
		{
			Stage:       db.LifecycleStage{ID: 102},
			Environment: db.Environment{ID: 202, Name: "shared"},
		},
		{
			Stage:       db.LifecycleStage{ID: 103},
			Environment: db.Environment{ID: 203},
		},
	}

	// When
	rendered := renderLifecycleStageList(t, stages)

	// Then
	for _, pattern := range []string{
		`(?s)id="lifecycle-stage-101"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage shared \(stage ID 101\) down"`,
		`(?s)id="lifecycle-stage-102"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage shared \(stage ID 102\) down"`,
		`(?s)id="lifecycle-stage-103"[^>]*>.*?data-lifecycle-stage-action="move-up"[^>]*aria-label="Move stage ID 103 up"`,
		`(?s)data-mobile-lifecycle-stage="101"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage shared \(stage ID 101\) down"`,
		`(?s)data-mobile-lifecycle-stage="102"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage shared \(stage ID 102\) down"`,
		`(?s)data-mobile-lifecycle-stage="103"[^>]*>.*?data-lifecycle-stage-action="move-up"[^>]*aria-label="Move stage ID 103 up"`,
	} {
		requireHTMLPattern(t, rendered, pattern)
	}
}

func TestLifecycleStageList_uses_native_HTMX_confirmation_for_each_delete_form(
	t *testing.T,
) {
	// Given
	stages := []pages.LifecycleStageView{{
		Stage:       db.LifecycleStage{ID: 101},
		Environment: db.Environment{ID: 201, Name: "production"},
	}}

	// When
	rendered := renderLifecycleStageList(t, stages)

	// Then
	confirmation := `hx-boost="true" ` +
		`hx-confirm="Remove this stage from the lifecycle?"`
	if count := strings.Count(rendered, confirmation); count != 2 {
		t.Errorf("confirmation count = %d, want 2", count)
	}
	if strings.Contains(rendered, "onsubmit=") {
		t.Error("lifecycle stage markup contains onsubmit")
	}
}

func TestLifecycleStageList_dispatches_one_mobile_update_after_successful_request(
	t *testing.T,
) {
	// Given
	stages := []pages.LifecycleStageView{{
		Stage:       db.LifecycleStage{ID: 101},
		Environment: db.Environment{ID: 201, Name: "production"},
	}}

	// When
	rendered := renderLifecycleStageList(t, stages)

	// Then
	expression := `x-on:htmx:after-request="if ($event.detail.successful) ` +
		`$dispatch('mobile-stage-updated')"`
	if count := strings.Count(rendered, expression); count != 1 {
		t.Errorf("successful update dispatch count = %d, want 1", count)
	}
	if strings.Contains(rendered, "hx-on:") {
		t.Error("lifecycle stage markup contains hx-on")
	}
	requireHTMLPattern(
		t,
		rendered,
		`(?s)id="lifecycle-stages"[^>]*x-data[^>]*`+
			`hx-trigger="mobile-stage-updated from:body"`,
	)
}

func TestLifecycleStageList_preserves_desktop_and_mobile_HTMX_swap_contracts(
	t *testing.T,
) {
	// Given
	stages := []pages.LifecycleStageView{{
		Stage:       db.LifecycleStage{ID: 101},
		Environment: db.Environment{ID: 201, Name: "production"},
	}}

	// When
	rendered := renderLifecycleStageList(t, stages)

	// Then
	for _, pattern := range []string{
		`(?s)data-lifecycle-stage-action="approval"[^>]*` +
			`hx-target="closest tr"[^>]*hx-swap="outerHTML"`,
		`(?s)data-lifecycle-stage-action="approval"[^>]*` +
			`hx-target="#lifecycle-stages"[^>]*hx-swap="none"`,
		`(?s)id="lifecycle-stages"[^>]*hx-target="this"[^>]*` +
			`hx-swap="outerHTML"[^>]*hx-select="#lifecycle-stages"`,
	} {
		requireHTMLPattern(t, rendered, pattern)
	}
}

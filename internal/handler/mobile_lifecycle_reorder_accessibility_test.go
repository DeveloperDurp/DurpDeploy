package handler_test

import (
	"bytes"
	"context"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/views/pages"
)

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
	var rendered bytes.Buffer

	// When
	err := pages.LifecycleStageList(db.Lifecycle{ID: 1}, stages, nil).
		Render(context.Background(), &rendered)

	// Then
	if err != nil {
		t.Fatalf("render lifecycle stages: %v", err)
	}
	for _, pattern := range []string{
		`(?s)id="lifecycle-stage-101"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage shared \(stage ID 101\) down"`,
		`(?s)id="lifecycle-stage-102"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage shared \(stage ID 102\) down"`,
		`(?s)id="lifecycle-stage-103"[^>]*>.*?data-lifecycle-stage-action="move-up"[^>]*aria-label="Move stage ID 103 up"`,
		`(?s)data-mobile-lifecycle-stage="101"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage shared \(stage ID 101\) down"`,
		`(?s)data-mobile-lifecycle-stage="102"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage shared \(stage ID 102\) down"`,
		`(?s)data-mobile-lifecycle-stage="103"[^>]*>.*?data-lifecycle-stage-action="move-up"[^>]*aria-label="Move stage ID 103 up"`,
	} {
		requireHTMLPattern(t, rendered.String(), pattern)
	}
}

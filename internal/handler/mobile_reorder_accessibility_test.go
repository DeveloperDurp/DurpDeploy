package handler_test

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestMobile_RenderedHTML_names_reorder_controls_when_authorized(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t).withWriterControls(t)
	pages := []struct {
		name     string
		path     string
		patterns []string
	}{
		{
			name: "steps",
			path: fmt.Sprintf(
				"/projects/%d/steps-page",
				fixture.project.ID,
			),
			patterns: []string{
				fmt.Sprintf(
					`(?s)id="step-row-%d"[^>]*>.*?data-step-action="move-down"[^>]*aria-label="Move step %s \(ID %d\) down"`,
					fixture.step.ID,
					fixture.step.Name,
					fixture.step.ID,
				),
				fmt.Sprintf(
					`(?s)id="step-row-%d"[^>]*>.*?data-step-action="move-up"[^>]*aria-label="Move step %s \(ID %d\) up"`,
					fixture.secondStep.ID,
					fixture.secondStep.Name,
					fixture.secondStep.ID,
				),
				fmt.Sprintf(
					`(?s)data-mobile-step="%d"[^>]*>.*?data-step-action="move-down"[^>]*aria-label="Move step %s \(ID %d\) down"`,
					fixture.step.ID,
					fixture.step.Name,
					fixture.step.ID,
				),
				fmt.Sprintf(
					`(?s)data-mobile-step="%d"[^>]*>.*?data-step-action="move-up"[^>]*aria-label="Move step %s \(ID %d\) up"`,
					fixture.secondStep.ID,
					fixture.secondStep.Name,
					fixture.secondStep.ID,
				),
			},
		},
		{
			name: "lifecycle stages",
			path: fmt.Sprintf("/lifecycles/%d", fixture.lifecycle.ID),
			patterns: []string{
				fmt.Sprintf(
					`(?s)id="lifecycle-stage-%d"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage mobile-structural-environment \(stage ID %d\) down"`,
					fixture.stage.ID,
					fixture.stage.ID,
				),
				fmt.Sprintf(
					`(?s)id="lifecycle-stage-%d"[^>]*>.*?data-lifecycle-stage-action="move-up"[^>]*aria-label="Move stage mobile-structural-second-environment \(stage ID %d\) up"`,
					fixture.secondStage.ID,
					fixture.secondStage.ID,
				),
				fmt.Sprintf(
					`(?s)data-mobile-lifecycle-stage="%d"[^>]*>.*?data-lifecycle-stage-action="move-down"[^>]*aria-label="Move stage mobile-structural-environment \(stage ID %d\) down"`,
					fixture.stage.ID,
					fixture.stage.ID,
				),
				fmt.Sprintf(
					`(?s)data-mobile-lifecycle-stage="%d"[^>]*>.*?data-lifecycle-stage-action="move-up"[^>]*aria-label="Move stage mobile-structural-second-environment \(stage ID %d\) up"`,
					fixture.secondStage.ID,
					fixture.secondStage.ID,
				),
			},
		},
	}

	for _, page := range pages {
		t.Run(page.name, func(t *testing.T) {
			// When
			body := fixture.getHTML(t, fixture.admin, page.path)

			// Then
			for _, pattern := range page.patterns {
				requireHTMLPattern(t, body, pattern)
			}
		})
	}
}

func TestMobile_RenderedHTML_hides_named_reorder_controls_when_viewer(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t).withWriterControls(t)
	pages := []struct {
		name string
		path string
	}{
		{
			name: "steps",
			path: fmt.Sprintf(
				"/projects/%d/steps-page",
				fixture.project.ID,
			),
		},
		{
			name: "lifecycle stages",
			path: fmt.Sprintf("/lifecycles/%d", fixture.lifecycle.ID),
		},
	}

	for _, page := range pages {
		t.Run(page.name, func(t *testing.T) {
			// When
			body := fixture.getHTML(t, fixture.viewer, page.path)

			// Then
			for _, marker := range []string{
				`data-step-action="move-`,
				`data-lifecycle-stage-action="move-`,
				`aria-label="Move step `,
				`aria-label="Move stage `,
			} {
				if strings.Contains(body, marker) {
					t.Errorf("viewer received reorder control %q", marker)
				}
			}
		})
	}
}

func TestMobile_RenderedHTML_keeps_duplicate_step_name_reorder_labels_unique(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t).withWriterControls(t)
	ctx := context.Background()
	duplicateName := fixture.step.Name
	secondStep := mustMobileStructural(
		t,
		mobileStructuralQuery(fixture.repo.Queries.UpdateStep(
			ctx,
			db.UpdateStepParams{
				ID:             fixture.secondStep.ID,
				Name:           duplicateName,
				ScriptBody:     fixture.secondStep.ScriptBody,
				SortOrder:      fixture.secondStep.SortOrder,
				TimeoutSeconds: fixture.secondStep.TimeoutSeconds,
				MaxRetries:     fixture.secondStep.MaxRetries,
			},
		)),
	)
	mustMobileStructural(
		t,
		mobileStructuralQuery(fixture.repo.Queries.CreateStep(
			ctx,
			db.CreateStepParams{
				ProjectID:  fixture.project.ID,
				Name:       "mobile-structural-final-step",
				ScriptBody: "printf structural-final-step",
				SortOrder:  3,
			},
		)),
	)
	firstLabel := fmt.Sprintf(
		"Move step %s (ID %d) down",
		duplicateName,
		fixture.step.ID,
	)
	secondLabel := fmt.Sprintf(
		"Move step %s (ID %d) down",
		duplicateName,
		secondStep.ID,
	)

	// When
	body := fixture.getHTML(
		t,
		fixture.admin,
		fmt.Sprintf("/projects/%d/steps-page", fixture.project.ID),
	)

	// Then
	if firstLabel == secondLabel {
		t.Fatal("duplicate steps have duplicate reorder labels")
	}
	for _, pattern := range []string{
		fmt.Sprintf(
			`(?s)id="step-row-%d"[^>]*>.*?data-step-action="move-down"[^>]*aria-label="%s"`,
			fixture.step.ID,
			regexp.QuoteMeta(firstLabel),
		),
		fmt.Sprintf(
			`(?s)id="step-row-%d"[^>]*>.*?data-step-action="move-down"[^>]*aria-label="%s"`,
			secondStep.ID,
			regexp.QuoteMeta(secondLabel),
		),
		fmt.Sprintf(
			`(?s)data-mobile-step="%d"[^>]*>.*?data-step-action="move-down"[^>]*aria-label="%s"`,
			fixture.step.ID,
			regexp.QuoteMeta(firstLabel),
		),
		fmt.Sprintf(
			`(?s)data-mobile-step="%d"[^>]*>.*?data-step-action="move-down"[^>]*aria-label="%s"`,
			secondStep.ID,
			regexp.QuoteMeta(secondLabel),
		),
	} {
		requireHTMLPattern(t, body, pattern)
	}
}

func TestMobile_RenderedHTML_names_unnamed_reorder_step_with_its_ID(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t).withWriterControls(t)
	ctx := context.Background()
	unnamedStep := mustMobileStructural(
		t,
		mobileStructuralQuery(fixture.repo.Queries.UpdateStep(
			ctx,
			db.UpdateStepParams{
				ID:             fixture.secondStep.ID,
				ScriptBody:     fixture.secondStep.ScriptBody,
				SortOrder:      fixture.secondStep.SortOrder,
				TimeoutSeconds: fixture.secondStep.TimeoutSeconds,
				MaxRetries:     fixture.secondStep.MaxRetries,
			},
		)),
	)
	label := fmt.Sprintf("Move step ID %d up", unnamedStep.ID)

	// When
	body := fixture.getHTML(
		t,
		fixture.admin,
		fmt.Sprintf("/projects/%d/steps-page", fixture.project.ID),
	)

	// Then
	for _, pattern := range []string{
		fmt.Sprintf(
			`(?s)id="step-row-%d"[^>]*>.*?data-step-action="move-up"[^>]*aria-label="%s"`,
			unnamedStep.ID,
			regexp.QuoteMeta(label),
		),
		fmt.Sprintf(
			`(?s)data-mobile-step="%d"[^>]*>.*?data-step-action="move-up"[^>]*aria-label="%s"`,
			unnamedStep.ID,
			regexp.QuoteMeta(label),
		),
	} {
		requireHTMLPattern(t, body, pattern)
	}
}

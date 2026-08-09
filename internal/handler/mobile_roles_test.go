package handler_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestMobile_RenderedHTML_hides_write_controls_and_secret_values_when_viewer(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t).withWriterControls(t)
	pages := []struct {
		name         string
		path         string
		readMarker   string
		writeMarkers []string
	}{
		{
			name: "steps",
			path: fmt.Sprintf(
				"/projects/%d/steps-page",
				fixture.project.ID,
			),
			readMarker: `data-mobile-step="`,
			writeMarkers: []string{
				`data-step-action="move-up"`,
				`data-step-action="move-down"`,
				`data-step-action="edit"`,
				`data-step-action="delete"`,
				`data-step-action="save-template"`,
				`data-mobile-step-editor=`,
			},
		},
		{
			name:       "lifecycle stages",
			path:       fmt.Sprintf("/lifecycles/%d", fixture.lifecycle.ID),
			readMarker: `data-mobile-lifecycle-stage="`,
			writeMarkers: []string{
				`data-lifecycle-stage-action="approval"`,
				`data-lifecycle-stage-action="move-up"`,
				`data-lifecycle-stage-action="move-down"`,
				`data-lifecycle-stage-action="delete"`,
			},
		},
		{
			name: "schedules",
			path: fmt.Sprintf(
				"/projects/%d/schedules",
				fixture.project.ID,
			),
			readMarker: `data-mobile-schedule="`,
			writeMarkers: []string{
				fmt.Sprintf(
					`href="/projects/%d/schedules/new"`,
					fixture.project.ID,
				),
				`data-schedule-action="edit"`,
				`data-schedule-action="toggle"`,
				`data-schedule-action="delete"`,
				`data-desktop-schedule-edit=`,
				`data-mobile-schedule-edit=`,
			},
		},
		{
			name: "variables",
			path: fmt.Sprintf(
				"/projects/%d/variables",
				fixture.project.ID,
			),
			readMarker: `data-mobile-variable="`,
			writeMarkers: []string{
				`data-variable-action="override"`,
				fmt.Sprintf(
					`hx-post="/projects/%d/variables"`,
					fixture.project.ID,
				),
				`data-variable-action="edit"`,
				`data-variable-action="delete"`,
				`data-desktop-variable-edit=`,
				`data-mobile-variable-edit=`,
			},
		},
	}

	for _, page := range pages {
		t.Run(page.name, func(t *testing.T) {
			// When
			body := fixture.getHTML(t, fixture.viewer, page.path)

			// Then
			if !strings.Contains(body, page.readMarker) {
				t.Errorf("viewer missing read marker %q", page.readMarker)
			}
			for _, marker := range page.writeMarkers {
				if strings.Contains(body, marker) {
					t.Errorf("viewer received write control %q", marker)
				}
			}
		})
	}

	for _, session := range []*authedSession{fixture.admin, fixture.viewer} {
		// When
		body := fixture.getHTML(
			t,
			session,
			fmt.Sprintf("/projects/%d/variables", fixture.project.ID),
		)

		// Then
		if strings.Contains(body, mobileStructuralSecret) {
			t.Error("secret value appears in rendered variables")
		}
		if !strings.Contains(body, "••••••••") {
			t.Error("secret mask is absent from rendered variables")
		}
	}
}

func TestMobile_RenderedHTML_renders_writer_controls_when_authorized(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t).withWriterControls(t)
	pages := []struct {
		name    string
		path    string
		markers []string
	}{
		{
			name: "steps",
			path: fmt.Sprintf(
				"/projects/%d/steps-page",
				fixture.project.ID,
			),
			markers: []string{
				fmt.Sprintf(
					`(?s)id="step-row-%d"[^>]*>.*?data-step-action="move-down"`,
					fixture.step.ID,
				),
				fmt.Sprintf(
					`(?s)id="step-row-%d"[^>]*>.*?data-step-action="move-up"`,
					fixture.secondStep.ID,
				),
				fmt.Sprintf(
					`(?s)data-mobile-step="%d"[^>]*>.*?data-step-action="move-down"`,
					fixture.step.ID,
				),
				fmt.Sprintf(
					`(?s)data-mobile-step="%d"[^>]*>.*?data-step-action="move-up"`,
					fixture.secondStep.ID,
				),
				`data-step-action="edit"`,
				`data-step-action="delete"`,
				`data-step-action="save-template"`,
			},
		},
		{
			name: "lifecycle stages",
			path: fmt.Sprintf("/lifecycles/%d", fixture.lifecycle.ID),
			markers: []string{
				fmt.Sprintf(
					`(?s)id="lifecycle-stage-%d"[^>]*>.*?data-lifecycle-stage-action="move-down"`,
					fixture.stage.ID,
				),
				fmt.Sprintf(
					`(?s)id="lifecycle-stage-%d"[^>]*>.*?data-lifecycle-stage-action="move-up"`,
					fixture.secondStage.ID,
				),
				fmt.Sprintf(
					`(?s)data-mobile-lifecycle-stage="%d"[^>]*>.*?data-lifecycle-stage-action="move-down"`,
					fixture.stage.ID,
				),
				fmt.Sprintf(
					`(?s)data-mobile-lifecycle-stage="%d"[^>]*>.*?data-lifecycle-stage-action="move-up"`,
					fixture.secondStage.ID,
				),
				`data-lifecycle-stage-action="approval"`,
				`data-lifecycle-stage-action="delete"`,
			},
		},
		{
			name: "variable override and create form",
			path: fmt.Sprintf(
				"/projects/%d/variables",
				fixture.project.ID,
			),
			markers: []string{
				fmt.Sprintf(
					`data-override-for="%s"`,
					fixture.variable.Name,
				),
				`data-variable-action="override"`,
				`data-variable-action="edit"`,
				`data-variable-action="delete"`,
				fmt.Sprintf(
					`hx-post="/projects/%d/variables"`,
					fixture.project.ID,
				),
			},
		},
		{
			name: "new schedule",
			path: fmt.Sprintf(
				"/projects/%d/schedules",
				fixture.project.ID,
			),
			markers: []string{
				fmt.Sprintf(
					`href="/projects/%d/schedules/new"`,
					fixture.project.ID,
				),
				`data-schedule-action="edit"`,
				`data-schedule-action="toggle"`,
				`data-schedule-action="delete"`,
			},
		},
	}

	for _, session := range []struct {
		name    string
		session *authedSession
	}{
		{name: "admin", session: fixture.admin},
		{name: "deployer", session: fixture.deployer},
	} {
		t.Run(session.name, func(t *testing.T) {
			for _, page := range pages {
				t.Run(page.name, func(t *testing.T) {
					// When
					body := fixture.getHTML(t, session.session, page.path)

					// Then
					for _, marker := range page.markers {
						requireHTMLPattern(t, body, marker)
					}
				})
			}
		})
	}
}

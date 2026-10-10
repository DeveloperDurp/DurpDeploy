package handler_test

import (
	"fmt"
	"strings"
	"testing"
)

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
				`id="step-edit-dialog"`,
				`data-step-action="save-template"`,
			},
		},
		{
			name: "step edit modal",
			path: fmt.Sprintf(
				"/projects/%d/steps/%d/edit?dialog=1",
				fixture.project.ID,
				fixture.step.ID,
			),
			markers: []string{
				`data-step-action="delete"`,
				`data-step-edit-form`,
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
					if page.name == "lifecycle stages" &&
						session.name == "deployer" {
						if strings.Contains(
							body,
							"data-lifecycle-stage-action=",
						) ||
							strings.Contains(body, `name="environment_id"`) {
							t.Error(
								"deployer received lifecycle stage controls",
							)
						}
						requireHTMLPattern(
							t,
							body,
							`data-mobile-lifecycle-stage="`,
						)
						return
					}
					for _, marker := range page.markers {
						requireHTMLPattern(t, body, marker)
					}
					if page.name == "steps" &&
						strings.Contains(body, `data-step-action="delete"`) {
						t.Error(
							"step list includes Delete outside the edit modal",
						)
					}
				})
			}
		})
	}
}

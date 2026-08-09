package handler_test

import (
	"fmt"
	"strings"
	"testing"
)

func TestMobile_RenderedHTML_renders_breakpoint_gated_variants_when_authenticated(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t)

	pages := []struct {
		name     string
		path     string
		patterns []string
	}{
		{
			name: "steps",
			path: fmt.Sprintf("/projects/%d/steps-page", fixture.project.ID),
			patterns: []string{
				desktopVariantPattern("table"),
				mobileVariantPattern("ol"),
				`data-mobile-step-list`,
				fmt.Sprintf(`data-mobile-step="%d"`, fixture.step.ID),
				fmt.Sprintf(`id="step-row-%d"`, fixture.step.ID),
				fmt.Sprintf(`data-mobile-step-editor="%d"`, fixture.step.ID),
				`aria-label="Deployment steps"`,
			},
		},
		{
			name: "lifecycle stages",
			path: fmt.Sprintf("/lifecycles/%d", fixture.lifecycle.ID),
			patterns: []string{
				desktopVariantPattern("table"),
				mobileVariantPattern("ol"),
				`data-mobile-lifecycle-stage-list`,
				fmt.Sprintf(
					`data-mobile-lifecycle-stage="%d"`,
					fixture.stage.ID,
				),
				fmt.Sprintf(`id="lifecycle-stage-%d"`, fixture.stage.ID),
				`aria-label="Promotion stages"`,
			},
		},
		{
			name: "schedules",
			path: fmt.Sprintf("/projects/%d/schedules", fixture.project.ID),
			patterns: []string{
				desktopVariantPattern("table"),
				mobileVariantPattern("ul"),
				`data-mobile-schedule-list`,
				fmt.Sprintf(`data-mobile-schedule="%d"`, fixture.schedule.ID),
				fmt.Sprintf(`id="schedule-row-%d"`, fixture.schedule.ID),
				fmt.Sprintf(
					`data-desktop-schedule-edit="%d"`,
					fixture.schedule.ID,
				),
				fmt.Sprintf(
					`data-mobile-schedule-edit="%d"`,
					fixture.schedule.ID,
				),
				`aria-label="Schedules"`,
			},
		},
		{
			name: "variables",
			path: fmt.Sprintf("/projects/%d/variables", fixture.project.ID),
			patterns: []string{
				desktopVariantPattern("table"),
				mobileVariantPattern("div"),
				`data-mobile-variable-list`,
				fmt.Sprintf(`data-mobile-variable="%d"`, fixture.variable.ID),
				fmt.Sprintf(`id="variable-row-%d"`, fixture.variable.ID),
				fmt.Sprintf(
					`data-desktop-variable-edit="%d"`,
					fixture.variable.ID,
				),
				fmt.Sprintf(
					`data-mobile-variable-edit="%d"`,
					fixture.variable.ID,
				),
				`aria-labelledby="mobile-scoped-variables"`,
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

func TestMobile_RenderedHTML_rendersLifecycleBackAndKeepsPermissionGoBack(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t)
	const lifecycleBack = `<a href="/lifecycles" class="btn btn-ghost btn-sm">Back</a>`

	// When
	detailBody := fixture.getHTML(
		t,
		fixture.admin,
		fmt.Sprintf("/lifecycles/%d", fixture.lifecycle.ID),
	)
	formBody := fixture.getHTML(t, fixture.admin, "/lifecycles/new")
	viewerFormBody := fixture.getHTML(t, fixture.viewer, "/lifecycles/new")

	// Then
	if strings.Count(detailBody, lifecycleBack) != 1 {
		t.Errorf(
			"lifecycle detail back control = %q, want exactly one %q",
			detailBody,
			lifecycleBack,
		)
	}
	requireHTMLPattern(
		t,
		detailBody,
		`(?s)<div class="flex justify-between items-start">\s*<div>.*?</div>\s*<div class="flex gap-2">.*?`+lifecycleBack,
	)
	const lifecycleFormHeader = `(?s)<div class="flex justify-between items-center">\s*<h1 class="text-3xl font-bold">New Lifecycle</h1>\s*<div class="flex gap-2">\s*<a href="/lifecycles" class="btn btn-ghost btn-sm">Back</a>\s*</div>\s*</div>`
	requireHTMLPattern(t, formBody, lifecycleFormHeader)
	const permissionGoBack = `<a href="/lifecycles" class="btn btn-ghost btn-sm">Go back</a>`
	if strings.Count(viewerFormBody, permissionGoBack) != 1 {
		t.Errorf(
			"viewer permission control = %q, want exactly one %q",
			viewerFormBody,
			permissionGoBack,
		)
	}
}

func TestMobile_RenderedHTML_renders_project_back_controls_when_authenticated(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t)
	back := fmt.Sprintf(
		`<a href="/projects/%d" class="btn btn-ghost btn-sm">Back</a>`,
		fixture.project.ID,
	)
	pages := []struct {
		name          string
		path          string
		headerPattern string
	}{
		{
			name: "steps",
			path: fmt.Sprintf("/projects/%d/steps-page", fixture.project.ID),
			headerPattern: fmt.Sprintf(
				`(?s)<div class="flex justify-between items-center">\s*<h1 class="text-3xl font-bold">Steps for .*?</h1>\s*<div class="flex gap-2">\s*<a href="/projects/%d" class="btn btn-ghost btn-sm">Back</a>`,
				fixture.project.ID,
			),
		},
		{
			name: "variables",
			path: fmt.Sprintf("/projects/%d/variables", fixture.project.ID),
			headerPattern: fmt.Sprintf(
				`(?s)<div class="flex justify-between items-center">\s*<h1 class="text-3xl font-bold">Variables.*?</h1>\s*<div class="flex gap-2">\s*<a href="/projects/%d" class="btn btn-ghost btn-sm">Back</a>`,
				fixture.project.ID,
			),
		},
		{
			name: "schedules",
			path: fmt.Sprintf("/projects/%d/schedules", fixture.project.ID),
			headerPattern: fmt.Sprintf(
				`(?s)<div class="flex flex-wrap items-center justify-between gap-2">\s*<h1 class="text-3xl font-bold">Schedules.*?</h1>\s*<div class="flex gap-2 ml-auto">\s*<a href="/projects/%d/schedules/new" class="btn btn-primary btn-sm">New Schedule</a>\s*<a href="/projects/%d" class="btn btn-ghost btn-sm">Back</a>`,
				fixture.project.ID,
				fixture.project.ID,
			),
		},
	}

	for _, page := range pages {
		t.Run(page.name, func(t *testing.T) {
			// When
			body := fixture.getHTML(t, fixture.admin, page.path)

			// Then
			if got := strings.Count(body, back); got != 1 {
				t.Errorf("back control count = %d, want 1 for %q", got, back)
			}
			requireHTMLPattern(t, body, page.headerPattern)
		})
	}
}

func TestMobile_RenderedHTML_preserves_disclosures_and_containment_when_authenticated(
	t *testing.T,
) {
	// Given
	fixture := newMobileStructuralFixture(t)

	pages := []struct {
		name     string
		path     string
		patterns []string
		contents []string
	}{
		{
			name: "step script and editor",
			path: fmt.Sprintf("/projects/%d/steps-page", fixture.project.ID),
			patterns: []string{
				fmt.Sprintf(
					`(?s)data-mobile-step="%d"[^>]*>.*?<details[^>]*>\s*<summary`,
					fixture.step.ID,
				),
				fmt.Sprintf(
					`(?s)data-mobile-step-editor="%d"[^>]*x-show="editing"[^>]*>.*?<form[^>]*hx-put="/projects/%d/steps/%d"`,
					fixture.step.ID,
					fixture.project.ID,
					fixture.step.ID,
				),
				fmt.Sprintf(
					`(?s)<div[^>]*%s`,
					breakpointClassPattern("font-mono", "overflow-hidden"),
				),
			},
			contents: []string{fixture.step.ScriptBody},
		},
		{
			name: "template script",
			path: "/templates",
			patterns: []string{
				disclosurePattern(
					fmt.Sprintf("template-script-%d", fixture.template.ID),
				),
				`(?s)<div[^>]*id="templates-list"[^>]*>.*?<div[^>]*class="[^"]*overflow-x-auto[^"]*"[^>]*>\s*<table[^>]*class="[^"]*table[^"]*"`,
			},
			contents: []string{fixture.template.ScriptBody},
		},
		{
			name: "template history script",
			path: fmt.Sprintf("/templates/%d/history", fixture.template.ID),
			patterns: []string{
				disclosurePattern("template-history-script"),
			},
			contents: []string{fixture.template.ScriptBody},
		},
		{
			name: "audit details",
			path: "/admin/audit",
			patterns: []string{
				disclosurePattern("audit-details"),
			},
			contents: []string{fixture.auditDetails},
		},
		{
			name: "project environment mini-table",
			path: "/projects",
			patterns: []string{
				`(?s)<div[^>]*data-project-environment-scroll[^>]*>\s*<table[^>]*class="[^"]*table[^"]*"`,
			},
		},
		{
			name: "notification modal",
			path: "/admin/notifications",
			patterns: []string{
				fmt.Sprintf(
					`data-modal-target="notification_modal_%d"`,
					fixture.notification.ID,
				),
				fmt.Sprintf(
					`(?s)<dialog[^>]*id="notification_modal_%d"`,
					fixture.notification.ID,
				),
			},
			contents: []string{fixture.notification.Message},
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
			for _, content := range page.contents {
				if !strings.Contains(body, content) {
					t.Errorf("missing disclosed content %q", content)
				}
			}
		})
	}
}

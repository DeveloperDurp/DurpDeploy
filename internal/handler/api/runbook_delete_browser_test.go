//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
)

func TestRunbookDeletionBrowserE2E(t *testing.T) {
	// Given: a completed runbook, or the retained running-server example.
	base, session := os.Getenv(
		"DURPDEPLOY_LIVE_BASE",
	), os.Getenv(
		"DURPDEPLOY_LIVE_SESSION",
	)
	path := os.Getenv("DURPDEPLOY_LIVE_RUNBOOK_DELETE")
	history := os.Getenv("DURPDEPLOY_LIVE_RUNBOOK_DELETE_HISTORY")
	var fixture *artifactE2E
	var busyPath string
	if path == "" {
		fixture = newArtifactE2E(t)
		base, session = fixture.baseURL, fixture.session
		saved := createDeletionRunbook(
			t,
			fixture,
			strings.Repeat("Obsolete runbook ", 5)+"<&>",
		)
		path = fmt.Sprintf(
			"/projects/%d/runbooks/%d",
			fixture.project.ID,
			saved.Runbook.ID,
		)
		var execution db.RunbookExecution
		if err := json.Unmarshal(fixture.api(t, "POST", "/api/v1"+path+"/executions",
			map[string]int64{"environment_id": fixture.environment.ID}, 201), &execution); err != nil {
			t.Fatal(err)
		}
		fixture.completion(t, execution.DeploymentID, events.RunbookSucceeded)
		history = fmt.Sprintf(
			"/projects/%d/runbooks/executions/%d",
			fixture.project.ID,
			execution.ID,
		)
		fixture.api(t, "POST", "/api/v1"+path+"/schedules", map[string]any{
			"environment_id": fixture.environment.ID, "cron": "0 3 * * *",
		}, 201)
		busy := createDeletionRunbook(t, fixture, "Unfinished runbook").Runbook
		busyPath = fmt.Sprintf(
			"/projects/%d/runbooks/%d",
			fixture.project.ID,
			busy.ID,
		)
		if _, _, err := fixture.h.repo.CreateRunbookExecution(t.Context(),
			repository.RunbookExecutionRequest{
				ProjectID: fixture.project.ID, RunbookID: busy.ID,
				EnvironmentID: fixture.environment.ID,
			}); err != nil {
			t.Fatal(err)
		}
	}
	if base == "" || session == "" || history == "" {
		t.Fatal(
			"runbook browser deletion requires its server, session, and history",
		)
	}
	b := startPackageBrowser(t, true)
	b.call(
		t,
		"Security.setIgnoreCertificateErrors",
		map[string]bool{"ignore": true},
		&struct{}{},
	)
	if fixture != nil {
		viewer := seedAPIUser(
			t,
			fixture.h.repo,
			"deletion-viewer@example.test",
			"viewer",
		)
		if err := fixture.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
			ProjectID: fixture.project.ID, UserID: viewer.ID, Role: "deployer",
		}); err != nil {
			t.Fatal(err)
		}
		viewerSession, csrf, err := auth.NewSessionToken()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fixture.h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
			ID: viewerSession, UserID: viewer.ID, CsrfToken: csrf,
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}); err != nil {
			t.Fatal(err)
		}
		b.setBackTestSession(t, base, viewerSession)
		b.navigateBackTest(t, base+path)
		b.wait(t, `document.querySelector('.page-header h1') &&
!document.querySelector('form[hx-post$="/delete"]')`)
		captureRunbookDeletion(t, b, "runbook-delete-viewer")
	}
	b.setBackTestSession(t, base, session)
	b.navigateBackTest(t, base+path)
	b.wait(t, `document.querySelector('form[hx-post$="/delete"] button')`)
	captureRunbookDeletion(t, b, "runbook-delete-ready")
	b.evaluate(t, `document.addEventListener('htmx:confirm', e => {
window.runbookConfirmation = e.detail.question; }); true`)

	// When: the user cancels the real native confirmation, then accepts it.
	confirmRunbookDeletion(t, b, path, false)
	b.wait(t, fmt.Sprintf("location.pathname === %q", path))
	t.Logf(
		"Cancelled native confirmation: %s",
		b.evaluate(t, "window.runbookConfirmation"),
	)
	if fixture != nil {
		fixture.api(t, "GET", "/api/v1"+path, nil, 200)
	}
	confirmRunbookDeletion(t, b, path, true)
	b.wait(
		t,
		fmt.Sprintf(
			"location.pathname === %q",
			path[:strings.LastIndex(path, "/")],
		),
	)
	b.wait(
		t,
		`document.readyState === 'complete' && document.querySelector('#page-content')`,
	)
	captureRunbookDeletion(t, b, "runbook-delete-list")
	// Then: history remains readable and its retry control is absent.
	b.navigateBackTest(t, base+history)
	b.wait(t, `document.querySelector('[data-deleted-runbook]')`)
	if string(
		b.evaluate(
			t,
			`document.querySelector('form[action$="/retry"]') === null`,
		),
	) != "true" {
		t.Fatal("deleted runbook history offers retry")
	}
	captureRunbookDeletion(t, b, "runbook-delete-history")
	if busyPath != "" {
		b.navigateBackTest(t, base+busyPath)
		confirmRunbookDeletion(t, b, busyPath, true)
		b.wait(
			t,
			`document.querySelector('[x-data="toast"]')?.innerText.includes('confirm cleanup')`,
		)
		if string(
			b.evaluate(t, fmt.Sprintf("location.pathname === %q", busyPath)),
		) != "true" {
			t.Fatal("blocked deletion navigated away")
		}
		captureRunbookDeletion(t, b, "runbook-delete-blocked")
	}
}

func confirmRunbookDeletion(
	t *testing.T,
	b *packageBrowser,
	path string,
	accept bool,
) {
	t.Helper()
	b.wire.events = nil
	selector := fmt.Sprintf("form[hx-post=%q] button", path+"/delete")
	b.evaluate(
		t,
		fmt.Sprintf(
			"setTimeout(() => document.querySelector(%q).click(), 0); true",
			selector,
		),
	)
	if err := b.wire.waitEvent("Page.javascriptDialogOpening", b.session); err != nil {
		t.Fatal(err)
	}
	b.call(
		t,
		"Page.handleJavaScriptDialog",
		map[string]bool{"accept": accept},
		&struct{}{},
	)
}

func captureRunbookDeletion(t *testing.T, b *packageBrowser, state string) {
	t.Helper()
	for _, width := range []int{375, 768, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.wait(t, `document.fonts.status === 'loaded'`)
		b.screenshot(t, fmt.Sprintf("%s-%d", state, width))
		if string(
			b.evaluate(t, "document.documentElement.scrollWidth <= innerWidth"),
		) != "true" {
			t.Fatalf("%s overflows at %d pixels", state, width)
		}
	}
}

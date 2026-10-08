//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestEnvironmentQueueBrowserE2E(t *testing.T) {
	// Given: real local execution blocks two later deployments.
	f, slow, fast := newQueueE2E(t)
	head := verificationDeploy(t, f, slow)
	waitVerificationLog(
		t,
		f,
		fmt.Sprintf("/api/v1/deployments/%d", head.ID),
		"queue-head",
	)
	middle := verificationDeploy(t, f, fast)
	next := verificationDeploy(t, f, fast)
	browser := startPackageBrowser(t)
	setSession := func(session string) {
		t.Helper()
		var cookie struct{ Success bool }
		browser.call(t, "Network.setCookie", map[string]any{
			"name": "session", "value": session, "url": f.baseURL, "httpOnly": true,
		}, &cookie)
		if !cookie.Success {
			t.Fatal("browser session was not established")
		}
	}
	navigate := func(id int64) {
		t.Helper()
		browser.call(t, "Page.navigate", map[string]string{
			"url": fmt.Sprintf("%s/deployments/%d", f.baseURL, id),
		}, &struct{}{})
		browser.wait(
			t,
			`document.readyState === 'complete' && window.htmx && document.querySelector('#status-badge')`,
		)
	}
	capture := func(state string) {
		t.Helper()
		for _, width := range []int{375, 768, 1280} {
			browser.call(
				t,
				"Emulation.setDeviceMetricsOverride",
				map[string]any{
					"width": width, "height": 900, "deviceScaleFactor": 1, "mobile": false,
				},
				&struct{}{},
			)
			browser.wait(
				t,
				`document.documentElement.scrollWidth <= innerWidth`,
			)
			browser.evaluate(
				t,
				`document.fonts.ready.then(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve(true)))))`,
			)
			browser.screenshot(
				t,
				fmt.Sprintf("environment-queue-%s-%d", state, width),
			)
		}
	}
	setSession(f.session)
	navigate(next.ID)
	browser.wait(
		t,
		`document.querySelector('#status-badge').innerText.includes('Queue position: 2')`,
	)
	capture("queued")
	// When: an earlier queued request is cancelled elsewhere.
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/cancel", middle.ID),
		nil,
		200,
	)
	// Then: polling updates position without navigation, and viewers have no write affordance.
	browser.wait(
		t,
		`document.querySelector('#status-badge').innerText.includes('Queue position: 1')`,
	)
	capture("position-updated")
	viewer := seedAPIUser(
		t,
		f.h.repo,
		"queue-browser-viewer@example.test",
		"viewer",
	)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: f.project.ID, UserID: viewer.ID, Role: "deployer",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: "queue-browser-viewer", UserID: viewer.ID, CsrfToken: f.csrf, ExpiresAt: 4102444800,
	}); err != nil {
		t.Fatal(err)
	}
	setSession("queue-browser-viewer")
	navigate(next.ID)
	if string(
		browser.evaluate(
			t,
			`document.querySelector('button[hx-post$="/cancel"]') === null`,
		),
	) != "true" {
		t.Fatal("viewer sees queued cancellation")
	}
	capture("viewer")
	// And: the browser can cancel queued work without disturbing active execution.
	setSession(f.session)
	navigate(next.ID)
	browser.evaluate(
		t,
		`document.querySelector('button[hx-post$="/cancel"]').click(); true`,
	)
	browser.wait(
		t,
		`document.querySelector('#status-badge .badge').innerText === 'cancelled'`,
	)
	browser.wait(
		t,
		`document.querySelector('button[hx-post$="/cancel"]') === null && document.querySelector('button[hx-post$="/redeploy"]') !== null`,
	)
	browser.wait(
		t,
		`Array.from(document.querySelectorAll('[x-data="toast"][x-show]')).every(element => getComputedStyle(element).display === 'none')`,
	)
	waitVerificationStatus(t, f, head.ID, "running")
	capture("cancelled")
	book := queueRunbook(t, f)
	execution := queueRunbookExecution(t, f, book)
	runbookURL := fmt.Sprintf(
		"%s/projects/%d/runbooks/executions/%d",
		f.baseURL,
		f.project.ID,
		execution.ID,
	)
	browser.call(
		t,
		"Page.navigate",
		map[string]string{"url": runbookURL},
		&struct{}{},
	)
	browser.wait(
		t,
		`document.querySelector('#status-badge')?.innerText.includes('Queue position: 1')`,
	)
	capture("runbook-queued")
	browser.wire.events = nil
	browser.evaluate(
		t,
		`setTimeout(() => document.querySelector('form[action$="/cancel"]').requestSubmit(), 100); true`,
	)
	if err := browser.wire.waitEvent("Page.frameStoppedLoading", browser.session); err != nil {
		t.Fatal(err)
	}
	browser.wait(
		t,
		`document.querySelector('#status-badge .badge')?.innerText === 'cancelled'`,
	)
	capture("runbook-cancelled")
	remaining := verificationDeploy(t, f, fast)
	navigate(remaining.ID)
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/cancel", head.ID),
		nil,
		200,
	)
	f.completion(t, remaining.ID, events.DeploymentSucceeded)
	browser.wait(
		t,
		`document.querySelector('#status-badge .badge').innerText === 'succeeded'`,
	)
	browser.wait(
		t,
		`document.querySelector('button[hx-post$="/cancel"]') === null && document.querySelector('button[hx-post$="/redeploy"]') !== null`,
	)
	capture("advanced")
}

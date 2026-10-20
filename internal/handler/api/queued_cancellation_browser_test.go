//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"

	"durpdeploy/internal/db"
)

func TestQueuedCancellationBrowserE2E(t *testing.T) {
	f, owner, queued := queuedCancellationFixture(t)
	b := startPackageBrowser(t)
	b.setStepLogSession(t, f.baseURL, f.session)
	address := fmt.Sprintf("%s/deployments/%d", f.baseURL, queued.ID)
	b.openStepLogPage(t, address)
	b.wait(
		t,
		`document.querySelector('[data-step-index="0"] .badge').textContent === 'Pending'`,
	)
	b.evaluate(
		t,
		`document.querySelector('button[hx-post$="/cancel"]').click(); true`,
	)
	check := `(() => {
 const panels = [...document.querySelectorAll('details[data-step-index]')]
   .filter(panel => panel.dataset.stepIndex !== '-1');
 return panels.length === 2 && panels.every(panel =>
   panel.querySelector('.badge').textContent === 'Not run: cancelled while queued' &&
   panel.textContent.includes('No output exists because this step never started.') &&
   !panel.innerText.includes('No output yet.'));
})()`
	b.wait(t, check)
	b.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).source === null`,
	)
	b.evaluate(
		t,
		`document.querySelectorAll('details[data-step-index]').forEach(panel => panel.open = true); true`,
	)
	b.captureStepLogs(t, "queued-cancelled-live")
	b.openStepLogPage(t, address)
	b.wait(t, check)
	b.evaluate(
		t,
		`document.querySelector('[data-step-index="0"] summary').focus(); true`,
	)
	for _, kind := range []string{"keyDown", "keyUp"} {
		text := ""
		if kind == "keyDown" {
			text = "\r"
		}
		b.call(t, "Input.dispatchKeyEvent", map[string]any{
			"type": kind, "key": "Enter", "code": "Enter",
			"windowsVirtualKeyCode": 13, "text": text,
		}, &struct{}{})
	}
	b.wait(t, `document.querySelector('[data-step-index="0"]').open`)
	b.captureStepLogs(t, "queued-cancelled-reloaded")
	viewer := seedAPIUser(
		t,
		f.h.repo,
		"queued-cancel-viewer@example.test",
		"viewer",
	)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: f.project.ID, UserID: viewer.ID, Role: "viewer",
	}); err != nil {
		t.Fatal(err)
	}
	const session = "queued-cancel-viewer"
	if _, err := f.h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: session, UserID: viewer.ID, CsrfToken: f.csrf, ExpiresAt: 4102444800,
	}); err != nil {
		t.Fatal(err)
	}
	b.setStepLogSession(t, f.baseURL, session)
	b.openStepLogPage(t, address)
	b.wait(t, check)
	b.evaluate(
		t,
		`document.querySelector('[data-step-index="0"] summary').click(); true`,
	)
	b.captureStepLogs(t, "queued-cancelled-viewer")
	assertQueuedCancellation(t, f, owner, queued)
}

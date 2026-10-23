//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestDeploymentStepLogsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	// Default credentials are not configured literals. Exercise the local
	// writer's newline framing, then inspect live and reloaded step panels.
	credentialOutput := "0Bearer synthetic-proof\n0ghp_" + strings.Repeat(
		"S",
		36,
	) +
		"\n0AKIAABCDEFGHIJKLMNOP\n0xoxb-synthetic-proof\n" +
		"0xoxa-synthetic-proof\n0xoxp-synthetic-proof\n"
	for _, script := range []string{
		"printf '<img src=x onerror=alert(1)>\\nfirst-only\\n0Bea'; sleep 1; " +
			"printf '%s' '" + credentialOutput[4:] + "'; sleep 7",
		"printf 'second-only\\n'; sleep 8",
	} {
		f.api(t, "POST", f.base()+"/steps", map[string]any{
			"name": "Same step name", "script_body": script,
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		}, 201)
	}
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "browser-steps"}, 201), &release)
	browser := startPackageBrowser(t)
	browser.setStepLogSession(t, f.baseURL, f.session)
	var deployment db.Deployment
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		f.base()+"/deployments",
		map[string]int64{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		},
		201,
	), &deployment)
	address := fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID)
	browser.openStepLogPage(t, address)
	browser.wait(
		t,
		`document.querySelector('[data-step-index="0"] .badge').textContent === 'Running' && document.querySelector('[role="status"]').textContent.includes('Step 1')`,
	)
	browser.captureStepLogs(t, "first-running")
	browser.wait(
		t,
		`document.querySelector('[data-step-index="0"]').textContent.includes('mode=container') && document.querySelector('[data-step-index="0"]').textContent.includes('host=')`,
	)
	browser.wait(
		t,
		`document.querySelector('[data-step-index="0"]').textContent.split('0[REDACTED]').length === 7`,
	)
	assertNoBrowserCredential := func() {
		t.Helper()
		if string(browser.evaluate(
			t,
			`!document.body.innerText.includes('synthetic-proof') && !document.body.innerText.includes('SSSSSSSS') && !document.body.innerText.includes('AKIAABCDEFGHIJKLMNOP')`,
		)) != "true" {
			t.Fatal("default credential escaped into a rendered step panel")
		}
	}
	assertNoBrowserCredential()
	// Reload and explicitly reconnect while the first step is active.
	browser.wire.events = nil
	browser.call(t, "Page.reload", struct{}{}, &struct{}{})
	if err := browser.wire.waitEvent("Page.frameStoppedLoading", browser.session); err != nil {
		t.Fatal(err)
	}
	browser.wait(
		t,
		`document.readyState === 'complete' && document.querySelector('[data-step-index="0"] .badge').textContent === 'Running'`,
	)
	browser.wait(
		t,
		`document.querySelector('[data-step-index="0"]').textContent.split('0[REDACTED]').length === 7`,
	)
	assertNoBrowserCredential()
	browser.evaluate(
		t,
		`(() => { const stream = Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')); stream.source.close(); stream.init(); return true; })()`,
	)
	browser.wait(
		t,
		`Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).source.readyState === 1`,
	)
	browser.wait(
		t,
		`document.querySelector('[data-step-index="1"] .badge').textContent === 'Running' && document.querySelector('[role="status"]').textContent.includes('Step 2')`,
	)
	if string(
		browser.evaluate(
			t,
			`document.querySelector('[data-step-index="0"]').textContent.includes('first-only') && !document.querySelector('[data-step-index="1"]').textContent.includes('first-only') && !document.querySelector('img[src="x"]')`,
		),
	) != "true" {
		t.Fatal("output escaped its step panel or became HTML")
	}
	browser.captureStepLogs(t, "second-running")
	f.verifyStreamingLogTimestamp(t, browser, deployment)
	browser.evaluate(
		t,
		`(() => { const summary = document.querySelector('[data-step-index="1"] summary'); summary.click(); summary.focus(); return true; })()`,
	)
	browser.captureStepLogs(t, "keyboard-focused")
	for _, kind := range []string{"keyDown", "keyUp"} {
		text := ""
		if kind == "keyDown" {
			text = "\r"
		}
		browser.call(t, "Input.dispatchKeyEvent", map[string]any{
			"type": kind, "key": "Enter", "code": "Enter",
			"windowsVirtualKeyCode": 13, "text": text,
		}, &struct{}{})
	}
	browser.wait(t, `!document.querySelector('[data-step-index="1"]').open`)
	browser.captureStepLogs(t, "keyboard-collapsed")
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	browser.wait(
		t,
		`document.querySelector('[data-step-index="1"] .badge').textContent === 'Succeeded' && document.querySelector('[data-step-index="1"]').textContent.includes('second-only')`,
	)
	if string(
		browser.evaluate(
			t,
			`(() => { const ids = [...document.querySelectorAll('[data-log-id]')].map(el => el.dataset.logId); return new Set(ids).size === ids.length; })()`,
		),
	) != "true" {
		t.Fatal("reload/reconnect duplicated log entries")
	}

	viewer := seedAPIUser(t, f.h.repo, "step-log-viewer@example.com", "viewer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{ProjectID: f.project.ID, UserID: viewer.ID, Role: "deployer"}); err != nil {
		t.Fatal(err)
	}
	session, csrf, err := auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{ID: session, UserID: viewer.ID, CsrfToken: csrf, ExpiresAt: time.Now().Add(time.Hour).Unix()}); err != nil {
		t.Fatal(err)
	}
	browser.setStepLogSession(t, f.baseURL, session)
	browser.openStepLogPage(t, address)
	if string(
		browser.evaluate(
			t,
			`document.querySelectorAll('button[hx-post]').length === 0 && document.querySelectorAll('details[data-step-index]').length === 3`,
		),
	) != "true" {
		t.Fatal("viewer logs missing or write controls exposed")
	}
	browser.evaluate(
		t,
		`(() => { document.querySelectorAll('details[data-step-index]').forEach(panel => { if (!panel.open) panel.querySelector('summary').click(); }); return true; })()`,
	)
	browser.wait(
		t,
		`document.querySelector('[data-step-index="1"]').textContent.includes('mode=container') && document.querySelector('[data-step-index="1"]').textContent.includes('container=')`,
	)
	browser.captureStepLogs(t, "viewer-completed")
	assertNoBrowserCredential()
}

func TestDeploymentStepLogsHistoricalQuietBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Historical quiet step", "script_body": "true",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/releases",
			map[string]string{"version": "historical-quiet"},
			201,
		),
		&release,
	)
	deployment, err := f.h.repo.Queries.CreateDeployment(
		t.Context(),
		db.CreateDeploymentParams{
			ReleaseID:     release.ID,
			EnvironmentID: f.environment.ID,
			Status:        "succeeded",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d", deployment.ID),
		nil,
		200,
	)
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d", deployment.ID),
		nil,
		200,
	)
	if !strings.Contains(page, "State unavailable") ||
		strings.Contains(page, ">Not run<") {
		t.Fatal("historical page inferred a step outcome without evidence")
	}
	browser := startPackageBrowser(t)
	browser.setStepLogSession(t, f.baseURL, f.session)
	browser.openStepLogPage(
		t,
		fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID),
	)
	browser.wait(
		t,
		`document.querySelector('[data-step-index="0"] .badge').textContent === 'State unavailable' && Alpine.$data(document.querySelector('[x-data^="deploymentStepLogs"]')).source === null`,
	)
}

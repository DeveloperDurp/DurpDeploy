//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestDeploymentStepLogsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	for _, script := range []string{
		"printf '<img src=x onerror=alert(1)>\\nfirst-only\\n'; sleep 8",
		"sleep 8; printf 'second-only\\n'",
	} {
		f.api(t, "POST", f.base()+"/steps", map[string]any{
			"name": "Same step name", "script_body": script,
			"container_image": "docker.io/library/bash:5.2",
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
	browser.evaluate(
		t,
		`document.querySelector('[data-step-index="1"] summary').focus(); true`,
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
	browser.captureStepLogs(t, "viewer-completed")
}

func (b *packageBrowser) setStepLogSession(
	t *testing.T,
	address, session string,
) {
	t.Helper()
	var result struct {
		Success bool `json:"success"`
	}
	b.call(
		t,
		"Network.setCookie",
		map[string]any{
			"name":     "session",
			"value":    session,
			"url":      address,
			"httpOnly": true,
		},
		&result,
	)
	if !result.Success {
		t.Fatal("browser session missing")
	}
}

func (b *packageBrowser) openStepLogPage(t *testing.T, address string) {
	t.Helper()
	b.wire.events = nil
	b.call(t, "Page.navigate", map[string]string{"url": address}, &struct{}{})
	if err := b.wire.waitEvent("Page.frameStoppedLoading", b.session); err != nil {
		t.Fatal(err)
	}
	b.wait(
		t,
		fmt.Sprintf(
			`location.href === %q && document.readyState === 'complete' && window.Alpine && document.querySelector('[x-data^="deploymentStepLogs"]')`,
			address,
		),
	)
}

func (b *packageBrowser) captureStepLogs(t *testing.T, name string) {
	t.Helper()
	for _, width := range []int{375, 768, 1280} {
		b.call(
			t,
			"Emulation.setDeviceMetricsOverride",
			map[string]any{
				"width":             width,
				"height":            1100,
				"deviceScaleFactor": 1,
				"mobile":            false,
			},
			&struct{}{},
		)
		if string(
			b.evaluate(t, `document.documentElement.scrollWidth <= innerWidth`),
		) != "true" {
			t.Fatal("step panels overflow viewport")
		}
		b.screenshot(t, fmt.Sprintf("step-logs-%s-%d", name, width))
	}
}

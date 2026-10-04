//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestVerificationPollingBrowserE2E(t *testing.T) {
	// Given: a terminal deployment and the stale verification state from a read.
	f := newVerificationE2E(t)
	configureVerification(t, f, "bash", "echo healthy", 5)
	deployment := verificationDeploy(t, f,
		verificationRelease(t, f, "browser-poll"))
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	setVerificationPollingState(t, f, deployment.ID, "pending")
	browser := startPackageBrowser(t)
	var cookie struct{ Success bool }
	browser.call(t, "Network.setCookie", map[string]any{
		"name": "session", "value": f.session, "url": f.baseURL,
		"httpOnly": true,
	}, &cookie)
	if !cookie.Success {
		t.Fatal("browser session was not established")
	}
	browser.call(t, "Page.navigate", map[string]string{
		"url": fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID),
	}, &struct{}{})
	const card = `document.querySelector('[hx-get$="/verification"]')`
	browser.wait(t, `document.readyState === 'complete' && window.htmx && `+
		card+`?.getAttribute('hx-trigger') === 'every 3s'`)
	browser.evaluate(t, "document.fonts.ready.then(() => true)")
	capture := func(status string) {
		for _, width := range []int{375, 768, 1280} {
			browser.call(
				t,
				"Emulation.setDeviceMetricsOverride",
				map[string]any{
					"width":             width,
					"height":            900,
					"deviceScaleFactor": 1,
					"mobile":            false,
				},
				&struct{}{},
			)
			browser.wait(
				t,
				"document.documentElement.scrollWidth <= innerWidth",
			)
			browser.evaluate(t, `new Promise(resolve => {
 requestAnimationFrame(() => requestAnimationFrame(() => resolve(true)));
})`)
			browser.screenshot(
				t,
				fmt.Sprintf("verification-poll-%s-%d", status, width),
			)
		}
	}
	capture("pending")
	// When: the authoritative check becomes terminal after the page loaded.
	setVerificationPollingState(t, f, deployment.ID, "succeeded")
	// Then: HTMX polling updates the status without a navigation or reload.
	browser.wait(t, card+`?.getAttribute('hx-trigger') === 'none' && `+
		card+`.innerText.includes('succeeded')`)
	capture("succeeded")
}

func TestVerificationRollbackBrowserE2E(t *testing.T) {
	// Given: the real web/API server and installed browser harness.
	f := newVerificationE2E(t)
	browser := startPackageBrowser(t)
	setSession := func(session string) {
		var cookie struct{ Success bool }
		browser.call(t, "Network.setCookie", map[string]any{
			"name": "session", "value": session, "url": f.baseURL,
			"httpOnly": true,
		}, &cookie)
		if !cookie.Success {
			t.Fatal("browser session was not established")
		}
	}
	setSession(f.session)
	capture := func(name, path, predicate string) {
		browser.call(
			t,
			"Page.navigate",
			map[string]string{"url": f.baseURL + path},
			&struct{}{},
		)
		browser.wait(
			t,
			"document.readyState === 'complete' && window.Alpine && ("+predicate+")",
		)
		for _, width := range []int{375, 768, 1280} {
			browser.call(
				t,
				"Emulation.setDeviceMetricsOverride",
				map[string]any{
					"width":             width,
					"height":            900,
					"deviceScaleFactor": 1,
					"mobile":            false,
				},
				&struct{}{},
			)
			browser.wait(
				t,
				"document.documentElement.scrollWidth <= innerWidth",
			)
			browser.evaluate(t, `new Promise(resolve => {
 window.scrollTo(0, 0);
 requestAnimationFrame(() => requestAnimationFrame(() => resolve(true)));
})`)
			browser.screenshot(
				t,
				fmt.Sprintf("verification-%s-%d", name, width),
			)
		}
	}
	capture(
		"environment-new",
		"/environments/new",
		"document.querySelector('[name=verification_type]') !== null && "+
			"document.getElementById('verification-help').textContent.includes('fixed server container')",
	)
	editPath := fmt.Sprintf("/environments/%d/edit", f.environment.ID)
	capture(
		"environment-edit",
		editPath,
		"document.querySelector('[name=verification_type]') !== null",
	)
	// When: the operator configures verification through the actual HTMX form.
	browser.evaluate(t, `(() => {
 const form = document.querySelector('form[hx-put]');
 form.querySelector('[name=verification_type]').value = 'bash';
 form.querySelector('[name=verification_target]').value = 'echo browser-verified';
 form.querySelector('[name=verification_timeout_seconds]').value = '5';
 form.requestSubmit(); return true;
})()`)
	browser.wait(t, "document.querySelector('form[hx-put]') === null")
	configured := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/environments/%d", f.environment.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(configured, "browser-verified") {
		t.Fatal("browser form did not save verification")
	}
	capture(
		"environment-bash",
		editPath,
		"document.querySelector('[name=verification_type]').value === 'bash'",
	)
	v1 := verificationRelease(t, f, "browser-v1")
	releasePath := fmt.Sprintf("/projects/%d/releases/%d", f.project.ID, v1.ID)
	capture(
		"release-unused",
		releasePath,
		"document.querySelector('form[action$=refresh]') !== null",
	)
	first := verificationDeploy(t, f, v1)
	f.completion(t, first.ID, events.DeploymentSucceeded)
	capture(
		"release-locked",
		releasePath,
		"document.querySelector('form[action$=refresh]') === null && document.body.innerText.includes('snapshot is immutable')",
	)
	capture(
		"succeeded",
		fmt.Sprintf("/deployments/%d", first.ID),
		"document.querySelector('[aria-live=polite]').innerText.includes('succeeded')",
	)
	configureVerification(
		t,
		f,
		"bash",
		"echo browser-verification-failed; exit 1",
		5,
	)
	v2 := verificationRelease(t, f, "browser-v2")
	second := verificationDeploy(t, f, v2)
	f.completion(t, second.ID, events.DeploymentFailed)
	capture(
		"failed",
		fmt.Sprintf("/deployments/%d", second.ID),
		"document.querySelector('[aria-live=polite]').innerText.includes('failed')",
	)
	rollbackPath := fmt.Sprintf("/deployments/%d/rollback", second.ID)
	capture(
		"rollback-confirmation",
		rollbackPath,
		"document.querySelector('[name=target_deployment_id]') !== null",
	)
	if string(
		browser.evaluate(
			t,
			"document.body.innerText.includes('browser-v1') && document.body.innerText.includes('browser-v2')",
		),
	) != "true" {
		t.Fatal("confirmation omitted exact versions")
	}

	// Then: a viewer can inspect the deployment but sees no rollback submit form.
	viewer := seedAPIUser(t, f.h.repo, "browser-viewer@example.test", "viewer")
	if err := f.h.repo.Queries.AddProjectMember(
		t.Context(),
		db.AddProjectMemberParams{
			ProjectID: f.project.ID,
			UserID:    viewer.ID,
			Role:      "deployer",
		},
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(
		t.Context(),
		db.CreateSessionParams{
			ID:        "verification-browser-viewer",
			UserID:    viewer.ID,
			CsrfToken: f.csrf,
			ExpiresAt: 4102444800,
		},
	); err != nil {
		t.Fatal(err)
	}
	setSession("verification-browser-viewer")
	capture(
		"viewer-rollback",
		rollbackPath,
		"document.body.innerText.includes('Viewers cannot')",
	)
	if string(
		browser.evaluate(
			t,
			"document.querySelector('[name=target_deployment_id]') === null",
		),
	) != "true" {
		t.Fatal("viewer sees rollback form")
	}
	setSession(f.session)

	// And: lifecycle and approval states are visible before submission.
	var lifecycle struct{ ID int64 }
	if err := json.Unmarshal(
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "Browser lifecycle"},
			201,
		),
		&lifecycle,
	); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lifecycle.ID},
		200,
	)
	capture(
		"rollback-blocked",
		rollbackPath,
		"document.body.innerText.includes('not part of the lifecycle')",
	)
	var stage struct{ ID int64 }
	if err := json.Unmarshal(
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
			map[string]any{
				"environment_id":    f.environment.ID,
				"sort_order":        1,
				"requires_approval": true,
			},
			201,
		),
		&stage,
	); err != nil {
		t.Fatal(err)
	}
	capture(
		"rollback-approval",
		rollbackPath,
		"document.body.innerText.includes('Approval is required')",
	)
	f.api(
		t,
		"PATCH",
		fmt.Sprintf("/api/v1/lifecycles/%d/stages/%d", lifecycle.ID, stage.ID),
		map[string]bool{"requires_approval": false},
		200,
	)
	configureVerification(t, f, "bash", "echo browser-rollback-verified", 5)
	capture(
		"rollback-ready",
		rollbackPath,
		"document.querySelector('[name=target_deployment_id]') !== null",
	)
	// When: the user confirms through the browser's native form submission.
	browser.wire.events = nil
	browser.evaluate(
		t,
		"setTimeout(() => document.querySelector('form[action$=rollback]').requestSubmit(), 100); true",
	)
	if err := browser.wire.waitEvent(
		"Page.frameStoppedLoading",
		browser.session,
	); err != nil {
		t.Fatal(err)
	}
	browser.wait(
		t,
		"location.pathname !== "+fmt.Sprintf(
			"%q",
			rollbackPath,
		)+" && location.pathname.startsWith('/deployments/') && document.body.innerText.includes('browser-rollback-verified')",
	)
	var latest db.Deployment
	var currentPath string
	if err := json.Unmarshal(
		browser.evaluate(t, "location.pathname"),
		&currentPath,
	); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(
		f.api(t, "GET", "/api/v1"+currentPath, nil, 200),
		&latest,
	); err != nil {
		t.Fatal(err)
	}
	f.completion(t, latest.ID, events.DeploymentSucceeded)
	capture(
		"rolled-back",
		fmt.Sprintf("/deployments/%d", latest.ID),
		"document.querySelector('[aria-live=polite]').innerText.includes('succeeded')",
	)
	capture(
		"rollback-unavailable",
		rollbackPath,
		"document.body.innerText.includes('Rollback unavailable')",
	)
}

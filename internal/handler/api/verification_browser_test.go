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
			browser.screenshot(
				t,
				fmt.Sprintf("verification-%s-%d", name, width),
			)
		}
	}
	capture(
		"environment-new",
		"/environments/new",
		"document.querySelector('[name=verification_type]') !== null",
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
	first := verificationDeploy(t, f, v1)
	f.completion(t, first.ID, events.DeploymentSucceeded)
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
	browser.evaluate(
		t,
		"setTimeout(() => document.querySelector('form[action$=rollback]').requestSubmit(), 100); true",
	)
	browser.wait(
		t,
		"location.pathname !== "+fmt.Sprintf(
			"%q",
			rollbackPath,
		)+" && location.pathname.startsWith('/deployments/') && document.body.innerText.includes('browser-rollback-verified')",
	)
	var list struct{ Items []db.Deployment }
	if err := json.Unmarshal(
		f.api(t, "GET", f.base()+"/deployments?limit=1", nil, 200),
		&list,
	); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatal("rollback deployment was not created")
	}
	latest := list.Items[0]
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

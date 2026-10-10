//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
)

func TestReleaseRefreshConflictStaysOnPageBrowserE2E(t *testing.T) {
	f := newVerificationE2E(t)
	release := verificationRelease(t, f, "refresh-conflict")
	pending, err := f.h.repo.CreateDeployment(t.Context(),
		db.CreateDeploymentParams{
			ReleaseID: release.ID, EnvironmentID: f.environment.ID,
			Status: "pending_approval",
		})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/projects/%d/releases/%d", f.project.ID, release.ID)
	f.api(t, "POST", "/api/v1"+path+"/refresh", nil, http.StatusConflict)
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
		"width": 375, "height": 900, "deviceScaleFactor": 1,
		"mobile": false,
	}, &struct{}{})
	b.navigateBackTest(t, f.baseURL+path)
	b.evaluate(t, `window.confirm=()=>true;
document.querySelector('form[action$="/refresh"]').requestSubmit(); true`)
	b.wait(
		t,
		`document.body.innerText.includes('This release has active or unconfirmed deployments') || location.pathname.endsWith('/refresh')`,
	)
	if string(b.evaluate(t, fmt.Sprintf(
		`location.pathname === %q && !!document.querySelector('form[action$="/refresh"]') && document.body.innerText.includes('Cancel the active deployment or wait for it to finish') && !!document.querySelector('#release-refresh-error a[href^="/deployments?project_id="]')`,
		path,
	))) != "true" {
		t.Fatalf(
			"refresh conflict navigated to the POST-only action or hid the reason: %s",
			b.evaluate(
				t,
				`JSON.stringify({path:location.pathname,form:!!document.querySelector('form[action$="/refresh"]'),error:document.querySelector('#release-refresh-error')?.outerHTML,text:document.body.innerText})`,
			),
		)
	}
	b.captureNavigation(t, "release-refresh-conflict")
	// Completing the deployment releases the lock; retry reaches the release page.
	if err := f.h.repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{
			ID: pending.Deployment.ID, Status: "cancelled",
		}); err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", "/api/v1"+path+"/refresh", nil, http.StatusOK)
	b.evaluate(
		t,
		`window.refreshForm=document.querySelector('form[action$="/refresh"]');
window.refreshForm.requestSubmit(); true`,
	)
	b.wait(
		t,
		`!!document.querySelector('form[action$="/refresh"]') && document.querySelector('form[action$="/refresh"]') !== window.refreshForm && !document.querySelector('.htmx-request, .htmx-settling')`,
	)
	if string(
		b.evaluate(t, fmt.Sprintf("location.pathname === %q", path)),
	) != "true" {
		t.Fatal("successful refresh did not stay on the release page")
	}
	// Other rejected boosted submissions keep the page and show plain-text errors.
	if _, err := f.h.repo.DB.ExecContext(
		t.Context(),
		`UPDATE releases SET steps_json='[{"name":"old","execution_target":"local"}]' WHERE id=?`,
		release.ID,
	); err != nil {
		t.Fatal(err)
	}
	b.evaluate(
		t,
		`document.querySelector('form[action$="/refresh"]').requestSubmit(); true`,
	)
	b.wait(
		t,
		`document.body.innerText.includes('legacy local step has no container image')`,
	)
	if string(
		b.evaluate(t, fmt.Sprintf("location.pathname === %q", path)),
	) != "true" {
		t.Fatal("plain-text refresh error changed the page URL")
	}
	// Failed GET navigation still opens the application's full error page.
	b.evaluate(
		t,
		`(() => { const a=document.createElement('a'); a.href='/missing-refresh-test'; a.setAttribute('hx-boost','true'); document.body.append(a); htmx.process(a); a.click(); return true; })()`,
	)
	b.wait(
		t,
		`location.pathname==='/missing-refresh-test' && document.body.innerText.includes('The page you are looking for does not exist.')`,
	)
}

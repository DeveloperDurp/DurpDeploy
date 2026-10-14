//go:build e2e && packagebrowser

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/artifact"
	"durpdeploy/internal/db"
)

func TestArtifactGateDeploymentFormBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":                   "Final gate",
		"script_body":            "true",
		"container_image":        "docker.io/library/bash:5.2",
		"approval_artifact_path": "plan",
		"approval_review_path":   "review",
		"approval_review_format": "summary",
	}, 201)
	release := verificationRelease(t, f, "invalid-final-gate")
	body := f.api(t, "POST", f.base()+"/deployments", map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	}, 422)
	if !strings.Contains(string(body), artifact.ErrGateConfig.Error()) {
		t.Fatalf("API did not return artifact validation error: %s", body)
	}

	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	page := fmt.Sprintf("/projects/%d/deploy?release_id=%d",
		f.project.ID, release.ID)
	for _, width := range []int{375, 768, 1280} {
		b.call(t, "Emulation.setDeviceMetricsOverride", map[string]any{
			"width": width, "height": 900,
			"deviceScaleFactor": 1, "mobile": false,
		}, &struct{}{})
		b.navigateBackTest(t, f.baseURL+page)
		b.wait(t, `window.Alpine && window.htmx &&
document.querySelector('form [name="environment_id"]')`)
		b.evaluate(t, fmt.Sprintf(`(() => {
const form = document.querySelector('[name="environment_id"]').form;
window.deploymentFormBeforeSubmit = form;
const environment = form.querySelector('[name="environment_id"]');
environment.value = %q;
environment.dispatchEvent(new Event('change', {bubbles: true}));
form.querySelector('[name="note"]').value = 'Keep this deployment note';
form.requestSubmit();
return true;
})()`, fmt.Sprint(f.environment.ID)))
		b.wait(t, fmt.Sprintf(`(() => {
const toast = document.querySelector('[x-data="toast"]');
return toast && Alpine.$data(toast).visible &&
Alpine.$data(toast).type === 'error' &&
Alpine.$data(toast).message.includes(%q);
})()`, artifact.ErrGateConfig.Error()))
		if string(b.evaluate(t, fmt.Sprintf(`(() => {
const form = document.querySelector('[name="environment_id"]').form;
return form === window.deploymentFormBeforeSubmit &&
location.pathname + location.search === %q &&
form.querySelector('[name="release_id"]').value === %q &&
form.querySelector('[name="environment_id"]').value === %q &&
form.querySelector('[name="note"]').value === 'Keep this deployment note' &&
document.documentElement.scrollWidth <= innerWidth;
})()`, page, fmt.Sprint(release.ID),
			fmt.Sprint(f.environment.ID)))) != "true" {
			t.Fatal("validation replaced the form or changed its values or URL")
		}
		b.screenshot(t, fmt.Sprintf("deployment-form-error-%d", width))
	}
	var deployments struct{ Items []db.Deployment }
	if err := json.Unmarshal(f.api(t, "GET", f.base()+"/deployments",
		nil, 200), &deployments); err != nil {
		t.Fatal(err)
	}
	if len(deployments.Items) != 0 {
		t.Fatalf("invalid form created a deployment: %+v", deployments)
	}

	// A valid release still opens its deployment after the boosted POST.
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Apply", "script_body": "true",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Agent delivery", "script_body": "true",
		"execution_target": "agent",
	}, 201)
	valid := verificationRelease(t, f, "valid-gate")
	b.navigateBackTest(t, fmt.Sprintf(
		"%s/projects/%d/deploy?release_id=%d",
		f.baseURL, f.project.ID, valid.ID,
	))
	b.wait(t, `window.htmx && document.querySelector('[name="note"]')`)
	b.evaluate(t, fmt.Sprintf(`(() => {
const form = document.querySelector('[name="environment_id"]').form;
const environment = form.querySelector('[name="environment_id"]');
environment.value = %q;
environment.dispatchEvent(new Event('change', {bubbles: true}));
form.querySelector('[name="note"]').value = 'Successful form submission';
form.requestSubmit();
return true;
})()`, fmt.Sprint(f.environment.ID)))
	b.wait(t, `location.pathname.startsWith('/deployments/') &&
document.querySelector('[x-data^="deploymentStepLogs"]')`)
	if err := json.Unmarshal(f.api(t, "GET", f.base()+"/deployments",
		nil, 200), &deployments); err != nil {
		t.Fatal(err)
	}
	if len(deployments.Items) != 1 ||
		deployments.Items[0].ReleaseID != valid.ID ||
		deployments.Items[0].Note.String != "Successful form submission" {
		t.Fatalf(
			"successful form created incorrect deployment: %+v",
			deployments,
		)
	}
	f.completion(t, deployments.Items[0].ID, "deployment_failed")
}

func TestArtifactGateDeploymentFormPermissionsBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Deploy", "script_body": "true",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "permissions-change")
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	page := fmt.Sprintf("/projects/%d/deploy?release_id=%d",
		f.project.ID, release.ID)
	b.navigateBackTest(t, f.baseURL+page)
	b.wait(t, `window.Alpine && window.htmx &&
document.querySelector('[name="environment_id"]')`)
	if _, err := f.h.repo.DB.ExecContext(t.Context(),
		"UPDATE users SET role='viewer' WHERE email='artifact-e2e@example.com'"); err != nil {
		t.Fatal(err)
	}
	b.evaluate(t, fmt.Sprintf(`(() => {
const form = document.querySelector('[name="environment_id"]').form;
window.deploymentFormBeforeSubmit = form;
const environment = form.querySelector('[name="environment_id"]');
environment.value = %q;
environment.dispatchEvent(new Event('change', {bubbles: true}));
form.querySelector('[name="note"]').value = 'Keep this note after access changes';
form.requestSubmit();
return true;
})()`, fmt.Sprint(f.environment.ID)))
	b.wait(t, `(() => {
const toast = document.querySelector('[x-data="toast"]');
return toast && Alpine.$data(toast).visible &&
Alpine.$data(toast).type === 'error' &&
document.querySelector('[name="note"]')?.value === 'Keep this note after access changes' &&
document.querySelector('[name="note"]').form === window.deploymentFormBeforeSubmit;
})()`)
	if string(b.evaluate(t, fmt.Sprintf(
		`location.pathname + location.search === %q`, page))) != "true" {
		t.Fatal("authorization toast changed the URL")
	}
	b.screenshot(t, "deployment-form-permissions-error")
}

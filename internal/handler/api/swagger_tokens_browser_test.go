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

func TestSwaggerTokensBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	other := seedProject(t, f.h.repo)
	private := seedProject(t, f.h.repo)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Swagger execution", "script_body": "echo swagger-token-ok",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/releases",
		map[string]string{"version": "swagger-v1"}, 201), &release)
	token := mintProjectToken(t, f, "viewer", other.ID, f.project.ID)
	b := startPackageBrowser(t, true)
	b.call(t, "Page.navigate", map[string]string{
		"url": f.baseURL + "/api/swagger/",
	}, &struct{}{})
	b.wait(t, `window.ui && document.querySelector('.auth-wrapper .authorize')`)
	b.evaluate(
		t,
		`document.querySelector('.auth-wrapper .authorize').click(); true`,
	)
	b.wait(t, `document.querySelector('#api_key_value')`)
	b.evaluate(t, fmt.Sprintf(`(() => {
const input = document.querySelector('#api_key_value');
Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')
 .set.call(input, %q);
input.dispatchEvent(new Event('input', {bubbles:true}));
return true;
})()`, token.ID))
	b.evaluate(
		t,
		`document.querySelector('.dialog-ux .authorize').click(); true`,
	)
	b.wait(t, `window.ui.authSelectors.authorized().size === 1`)
	b.evaluate(
		t,
		`document.querySelector('.dialog-ux .btn-done').click(); true`,
	)
	list := func(total int) {
		t.Helper()
		var page struct {
			Items []struct{ ID int64 }
			Total int
		}
		decodeStepLogTest(t, b.swaggerTokenRequest(t, "projects-listProjects",
			f.baseURL+"/api/v1/projects", "", "", 200), &page)
		if page.Total != total || len(page.Items) != total {
			t.Fatal("Swagger project list did not match current token grants")
		}
		for _, item := range page.Items {
			if item.ID == private.ID ||
				(total == 1 && item.ID != f.project.ID) {
				t.Fatal("Swagger returned an unselected project")
			}
		}
	}
	list(2)
	b.swaggerTokenRequest(t, "projects-getProject",
		fmt.Sprintf("%s/api/v1/projects/%d", f.baseURL, private.ID),
		fmt.Sprint(private.ID), "", 403)
	body, err := json.Marshal(map[string]int64{
		"release_id": release.ID, "environment_id": f.environment.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	b.swaggerTokenRequest(t, "deployments-createDeployment",
		f.baseURL+f.base()+"/deployments", fmt.Sprint(f.project.ID),
		string(body), 403)
	var before struct{ Total int }
	decodeStepLogTest(t, f.api(t, "GET", f.base()+"/deployments", nil, 200),
		&before)
	if before.Total != 0 {
		t.Fatal("Viewer request created a deployment")
	}
	f.api(t, "PATCH", "/api/v1/tokens/"+token.TokenID, map[string]any{
		"scope": "scoped", "role": "deployer",
		"project_ids": []int64{f.project.ID},
	}, 200)
	// Keep the original authorization value to test immediate grant changes.
	list(1)
	b.swaggerTokenRequest(t, "projects-getProject",
		fmt.Sprintf("%s/api/v1/projects/%d", f.baseURL, other.ID),
		fmt.Sprint(other.ID), "", 403)
	var deployment db.Deployment
	decodeStepLogTest(t, b.swaggerTokenRequest(t,
		"deployments-createDeployment", f.baseURL+f.base()+"/deployments",
		fmt.Sprint(f.project.ID), string(body), 201), &deployment)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	var saved db.Deployment
	decodeStepLogTest(t, b.swaggerTokenRequest(t, "deployments-getDeployment",
		fmt.Sprintf("%s/api/v1/deployments/%d", f.baseURL, deployment.ID),
		fmt.Sprint(deployment.ID), "", 200), &saved)
	if saved.Status != "succeeded" || saved.ReleaseID != release.ID {
		t.Fatal("Swagger deployment did not execute its saved release")
	}
	logs := f.api(t, "GET", fmt.Sprintf(
		"/api/v1/deployments/%d/logs", deployment.ID), nil, 200)
	if !strings.Contains(string(logs), "swagger-token-ok") {
		t.Fatal("Swagger execution did not retain its script output")
	}
	f.api(t, "DELETE", "/api/v1/tokens/"+token.TokenID, nil, 204)
	b.swaggerTokenRequest(t, "projects-listProjects",
		f.baseURL+"/api/v1/projects", "", "", 401)
	b.swaggerTokenRequest(t, "deployments-createDeployment",
		f.baseURL+f.base()+"/deployments", fmt.Sprint(f.project.ID),
		string(body), 401)
	var after struct{ Total int }
	decodeStepLogTest(t, f.api(t, "GET", f.base()+"/deployments", nil, 200),
		&after)
	if after.Total != 1 {
		t.Fatal("revoked Swagger request changed the deployment count")
	}
	b.evaluate(t, `window.ui.authActions.logout(['bearer']); true`)
}

func (b *packageBrowser) swaggerTokenRequest(
	t *testing.T, operation, requestURL, id, body string, status int,
) []byte {
	t.Helper()
	selector := "#operations-" + operation
	b.evaluate(t, fmt.Sprintf(`(() => {
const operation = document.querySelector(%q);
if (!operation.classList.contains('is-open'))
 operation.querySelector('.opblock-summary-control').click();
return true;
})()`, selector))
	b.wait(
		t,
		fmt.Sprintf(`document.querySelector(%q)`, selector+" .try-out__btn"),
	)
	b.evaluate(t, fmt.Sprintf(`(() => {
const operation = document.querySelector(%q);
if (!operation.querySelector('.execute'))
 operation.querySelector('.try-out__btn').click();
operation.querySelector('.btn-clear')?.click();
return true;
})()`, selector))
	b.wait(t, fmt.Sprintf(`document.querySelector(%q)`, selector+" .execute"))
	b.wait(t, fmt.Sprintf(`!document.querySelector(%q)`,
		selector+" .live-responses-table tbody"))
	b.evaluate(t, fmt.Sprintf(`(() => {
const operation = document.querySelector(%q);
const set = (element, value, prototype) => {
 Object.getOwnPropertyDescriptor(prototype, 'value').set.call(element, value);
 element.dispatchEvent(new Event('input', {bubbles:true}));
};
if (%q) set(operation.querySelector('tr[data-param-name="id"] input'),
 %q, HTMLInputElement.prototype);
if (%q) set(operation.querySelector('textarea.body-param__text'),
 %q, HTMLTextAreaElement.prototype);
return true;
})()`, selector, id, id, body, body))
	b.wait(t, fmt.Sprintf(`(() => {
const operation = document.querySelector(%q);
const values = window.ui.specSelectors.parameterValues([
 operation.querySelector('.opblock-summary-path').textContent.trim(),
 operation.querySelector('.opblock-summary-method').textContent.trim().toLowerCase()
]).toJS();
return (!%q || values['path.id'] === %q) &&
 (!%q || Object.values(values).includes(%q));
})()`, selector, id, id, body, body))
	b.evaluate(t, fmt.Sprintf(`document.querySelector(%q).click(); true`,
		selector+" .execute"))
	b.wait(t, fmt.Sprintf(`(() => {
const operation = document.querySelector(%q);
return parseInt(operation.querySelector('.live-responses-table tbody .response-col_status')
 ?.textContent, 10) === %d &&
 operation.querySelector('.request-url pre')?.textContent.trim() === %q;
})()`, selector, status, requestURL))
	t.Logf("Swagger %s returned %d", requestURL, status)
	return b.evaluate(
		t,
		fmt.Sprintf(`JSON.parse(document.querySelector(%q).textContent)`,
			selector+" .live-responses-table tbody .microlight"),
	)
}

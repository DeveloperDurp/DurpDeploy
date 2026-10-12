//go:build e2e && packagebrowser

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestLifecycleLiveScopeBrowserE2E(t *testing.T) {
	f := newArtifactE2E(t)
	dev := seedEnv(t, f.h.repo)
	var lc struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Live browser scope"}, 201), &lc)
	for i, env := range []db.Environment{f.environment, dev} {
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/lifecycles/%d/stages", lc.ID),
			map[string]int64{
				"environment_id": env.ID,
				"sort_order":     int64(i + 1),
			},
			201,
		)
	}
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lc.ID,
	}, 200)
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/variables", lc.ID),
		map[string]string{"name": "LIVE", "value": "all-stages"}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":            "Print scope",
		"script_body":     `printf 'LIVE=%s\n' "${LIVE-unset}"`,
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "before-shared-scope-edit")
	b := startPackageBrowser(t)
	b.setBackTestSession(t, f.baseURL, f.session)
	b.navigateBackTest(t, fmt.Sprintf("%s/lifecycles/%d", f.baseURL, lc.ID))
	b.evaluate(
		t,
		`[...document.querySelectorAll('[data-shared-variable="LIVE"] a')].find(e=>e.getClientRects().length).click(); true`,
	)
	b.wait(
		t,
		`document.querySelector('#shared-name')?.value === 'LIVE' && !document.querySelector('.htmx-request,.htmx-settling')`,
	)
	b.evaluate(
		t,
		fmt.Sprintf(
			`document.querySelector('#shared-environment').value=%q; document.querySelector('#shared-value').value='dev-only'; document.querySelector('[data-shared-variable-form]').requestSubmit(); true`,
			fmt.Sprint(dev.ID),
		),
	)
	b.wait(
		t,
		`!!document.querySelector('[data-shared-variable="LIVE"]') && !document.querySelector('[data-shared-variable-form][hx-put]') && !document.querySelector('.htmx-request,.htmx-settling')`,
	)
	b.captureNavigation(t, "lifecycle-live-dev-scope")
	for _, test := range []struct {
		environment int64
		want        string
	}{{f.environment.ID, "LIVE=unset"}, {dev.ID, "LIVE=dev-only"}} {
		var deployment db.Deployment
		decodeLifecycleTest(
			t,
			f.api(t, "POST", f.base()+"/deployments", map[string]int64{
				"release_id": release.ID, "environment_id": test.environment,
			}, 201),
			&deployment,
		)
		f.completion(t, deployment.ID, events.DeploymentSucceeded)
		logs := string(
			f.api(
				t,
				"GET",
				fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
				nil,
				200,
			),
		)
		if !strings.Contains(logs, test.want) {
			t.Fatalf("scope edit did not affect existing release: %s", logs)
		}
		b.navigateBackTest(
			t,
			fmt.Sprintf("%s/deployments/%d", f.baseURL, deployment.ID),
		)
		b.wait(
			t,
			fmt.Sprintf(`document.body.textContent.includes(%q)`, test.want),
		)
	}
	b.captureNavigation(t, "lifecycle-live-dev-deployment")
}

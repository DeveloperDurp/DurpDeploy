//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestLifecycleVariableScopeChangesApplyToExistingReleaseE2E(t *testing.T) {
	// Given: a release created while LIVE applies to all lifecycle environments.
	f := newArtifactE2E(t)
	dev := seedEnv(t, f.h.repo)
	var lifecycle struct{ ID int64 }
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Live scope"}, 201), &lifecycle)
	for order, env := range []db.Environment{f.environment, dev} {
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
			map[string]int64{
				"environment_id": env.ID,
				"sort_order":     int64(order + 1),
			},
			201,
		)
	}
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	var variable struct{ ID int64 }
	path := fmt.Sprintf("/api/v1/lifecycles/%d/variables", lifecycle.ID)
	decodeLifecycleTest(t, f.api(
		t,
		"POST",
		path,
		map[string]string{
			"name":  "LIVE",
			"value": "all-environments",
		},
		201,
	), &variable)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name":            "Print scope",
		"script_body":     `printf 'LIVE=%s\n' "${LIVE-unset}"`,
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "live-scope")
	first := verificationDeploy(t, f, release)
	f.completion(t, first.ID, events.DeploymentSucceeded)
	// When: changing the shared variable to dev through the web endpoint.
	f.web(
		t,
		"PUT",
		fmt.Sprintf("/lifecycles/%d/variables/%d", lifecycle.ID, variable.ID),
		url.Values{"name": {"LIVE"}, "value": {"dev-only"},
			"environment_id": {fmt.Sprint(dev.ID)}},
		303,
	)
	second := verificationDeploy(t, f, release)
	f.completion(t, second.ID, events.DeploymentSucceeded)
	// Then: another deployment of the same release omits the variable outside dev.
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", second.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(logs, "LIVE=unset") ||
		strings.Contains(logs, "all-environments") {
		t.Fatalf(
			"deployment reused the release's old lifecycle scope: %s",
			logs,
		)
	}
	var devDeployment db.Deployment
	decodeLifecycleTest(
		t,
		f.api(t, "POST", f.base()+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": dev.ID,
		}, 201),
		&devDeployment,
	)
	f.completion(t, devDeployment.ID, events.DeploymentSucceeded)
	logs = string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", devDeployment.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(logs, "LIVE=dev-only") {
		t.Fatalf("deployment did not use the current dev value: %s", logs)
	}
}

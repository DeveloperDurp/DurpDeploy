//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

type releaseVariableGuidanceFixture struct {
	f               *artifactE2E
	release         db.Release
	projectVariable int64
	sharedPath      string
}

func newReleaseVariableGuidance(t *testing.T) releaseVariableGuidanceFixture {
	t.Helper()
	f := newArtifactE2E(t)
	var lifecycle, shared, local struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "Variable guidance"}, 201), &lifecycle)
	base := fmt.Sprintf("/api/v1/lifecycles/%d", lifecycle.ID)
	f.api(t, "POST", base+"/stages", map[string]int64{
		"environment_id": f.environment.ID, "sort_order": 1,
	}, 201)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	decodeStepLogTest(t, f.api(t, "POST", base+"/variables",
		map[string]string{"name": "SHARED", "value": "initial"}, 201), &shared)
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/variables",
		map[string]string{"name": "VALUE", "value": "old"}, 201), &local)
	f.api(t, "POST", f.base()+"/variables", map[string]any{
		"name": "SECRET", "value": "guidance-secret-sentinel", "secret": true,
	}, 201)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name":            "Print variable sources",
		"script_body":     `printf 'project=%s shared=%s\n' "$VALUE" "$SHARED"`,
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"variable_names":  []string{"VALUE", "SHARED"},
	}, 201)
	return releaseVariableGuidanceFixture{
		f: f, release: verificationRelease(t, f, "variable-guidance-v1"),
		projectVariable: local.ID,
		sharedPath:      fmt.Sprintf("%s/variables/%d", base, shared.ID),
	}
}

func (fixture releaseVariableGuidanceFixture) change(t *testing.T) {
	t.Helper()
	f := fixture.f
	f.api(
		t,
		"PUT",
		fmt.Sprintf("%s/variables/%d", f.base(), fixture.projectVariable),
		map[string]string{"name": "VALUE", "value": "new"},
		200,
	)
	f.api(t, "PUT", fixture.sharedPath,
		map[string]string{"name": "SHARED", "value": "current"}, 200)
}

func (fixture releaseVariableGuidanceFixture) path() string {
	return fmt.Sprintf("%s/releases/%d", fixture.f.base(), fixture.release.ID)
}

func (fixture releaseVariableGuidanceFixture) checkSnapshot(
	t *testing.T,
	want string,
) {
	t.Helper()
	var snapshot struct {
		Variables []struct{ Name, Value string }
	}
	decodeStepLogTest(
		t,
		fixture.f.api(t, "GET", fixture.path(), nil, 200),
		&snapshot,
	)
	values := make(map[string]string)
	for _, variable := range snapshot.Variables {
		values[variable.Name] = variable.Value
	}
	secret, hasSecret := values["SECRET"]
	if len(values) != 2 || values["VALUE"] != want || !hasSecret ||
		secret != "" {
		t.Fatalf("release snapshot changed or exposed a secret: %+v", values)
	}
}

func (fixture releaseVariableGuidanceFixture) checkExecution(
	t *testing.T,
	local string,
) {
	t.Helper()
	f := fixture.f
	deployment := verificationDeploy(t, f, fixture.release)
	f.completion(t, deployment.ID, events.DeploymentSucceeded)
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
		nil,
		200,
	)
	if !strings.Contains(string(logs), "project="+local+" shared=current") ||
		strings.Contains(string(logs), "guidance-secret-sentinel") {
		t.Fatal("deployment did not use the documented variable sources")
	}
}

func TestReleaseVariableGuidanceAPIWebE2E(t *testing.T) {
	fixture := newReleaseVariableGuidance(t)
	fixture.change(t)
	fixture.checkSnapshot(t, "old")
	fixture.checkExecution(t, "old")
	f := fixture.f
	page := f.web(
		t,
		"GET",
		strings.TrimPrefix(fixture.path(), "/api/v1"),
		nil,
		200,
	)
	for _, marker := range []string{"data-release-project-guidance", "data-release-lifecycle-guidance"} {
		if !strings.Contains(page, marker) {
			t.Fatal("release guidance does not separate variable sources")
		}
	}
	f.api(t, "POST", fixture.path()+"/refresh", nil, 200)
	fixture.checkSnapshot(t, "new")
	fixture.checkExecution(t, "new")
}

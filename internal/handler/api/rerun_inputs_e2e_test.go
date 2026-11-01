//go:build e2e

package api_test

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

type rerunInputsFixture struct {
	f        *artifactE2E
	original db.Deployment
	logs     []byte
}

func newRerunInputsFixture(t *testing.T) rerunInputsFixture {
	return newRerunInputsSource(t, false)
}

func newRerunInputsSource(t *testing.T, failFirst bool) rerunInputsFixture {
	t.Helper()
	f := newArtifactE2E(t)
	var lifecycle struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "rerun lifecycle"}, 201), &lifecycle)
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
		map[string]int64{"environment_id": f.environment.ID}, 201)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	lifecycleVariables := fmt.Sprintf(
		"/api/v1/lifecycles/%d/variables",
		lifecycle.ID,
	)
	var shared struct{ ID int64 }
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		lifecycleVariables,
		map[string]string{
			"name":  "SHARED_VALUE",
			"value": "shared-one",
		},
		201,
	), &shared)
	script := `printf 'original-script:%s lifecycle:%s\n' "$A10_VALUE" "$SHARED_VALUE"`
	if failFirst {
		script += `; test "$A10_VALUE" != base-one`
	}
	var step struct {
		ID   int64
		Name string
	}
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Original step", "script_body": script,
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"variable_names":  []string{"A10_VALUE", "SHARED_VALUE"},
	}, 201), &step)
	var variable struct{ ID int64 }
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		f.base()+"/variables",
		map[string]string{
			"name":  "A10_VALUE",
			"value": "base-one",
		},
		201,
	), &variable)
	release := verificationRelease(t, f, "rerun-inputs-v1")
	original := verificationDeploy(t, f, release)
	if failFirst {
		f.completion(t, original.ID, events.DeploymentFailed)
	} else {
		f.completion(t, original.ID, events.DeploymentSucceeded)
	}
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", original.ID),
		nil,
		200,
	)
	if !strings.Contains(string(logs), "original-script:base-one") {
		t.Fatal("original deployment did not use its release variables")
	}
	f.api(t, "PUT", fmt.Sprintf("%s/%d", lifecycleVariables, shared.ID),
		map[string]string{"name": "SHARED_VALUE", "value": "shared-two"}, 200)
	variablePath := fmt.Sprintf("%s/variables/%d", f.base(), variable.ID)
	f.api(t, "PUT", variablePath, map[string]string{
		"name": "A10_VALUE", "value": "base-two",
	}, 200)
	f.api(
		t,
		"PUT",
		fmt.Sprintf("%s/steps/%d", f.base(), step.ID),
		map[string]any{
			"name": step.Name, "script_body": `printf 'refreshed-script:%s\n' "$A10_VALUE"`,
			"execution_target": "local", "interpreter": "bash",
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
			"variable_names":  []string{"A10_VALUE"},
		},
		200,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/releases/%d/refresh", f.base(), release.ID),
		nil,
		200,
	)
	// Unsnapshotted project edits must not replace the release's current value.
	f.api(t, "PUT", variablePath, map[string]string{
		"name": "A10_VALUE", "value": "base-three",
	}, 200)
	return rerunInputsFixture{f: f, original: original, logs: logs}
}

func (fixture rerunInputsFixture) check(t *testing.T, next db.Deployment) {
	t.Helper()
	f := fixture.f
	if next.ID == fixture.original.ID ||
		next.ReleaseID != fixture.original.ReleaseID ||
		next.EnvironmentID != fixture.original.EnvironmentID {
		t.Fatal("re-run changed its release, environment, or original identity")
	}
	f.completion(t, next.ID, events.DeploymentSucceeded)
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", next.ID),
		nil,
		200,
	)
	if !strings.Contains(
		string(logs),
		"original-script:base-two lifecycle:shared-two",
	) ||
		strings.Contains(string(logs), "refreshed-script") ||
		strings.Contains(string(logs), "base-three") {
		t.Fatal(
			"re-run did not preserve original steps with current release variables",
		)
	}
	after := f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", fixture.original.ID),
		nil,
		200,
	)
	if !bytes.Equal(fixture.logs, after) {
		t.Fatal("re-run changed original deployment logs")
	}
}

func TestRerunInputsAPIWebE2E(t *testing.T) {
	for _, action := range []string{"redeploy", "retry"} {
		t.Run(action, func(t *testing.T) {
			fixture := newRerunInputsSource(t, action == "retry")
			f := fixture.f
			var next db.Deployment
			decodeStepLogTest(t, f.api(t, "POST", fmt.Sprintf(
				"/api/v1/deployments/%d/%s", fixture.original.ID, action,
			), nil, 201), &next)
			if action == "retry" && (!next.Note.Valid ||
				next.Note.String != fmt.Sprintf("Retry of #%d", fixture.original.ID)) {
				t.Fatal("retry lost its source note")
			}
			fixture.check(t, next)
		})
	}
}

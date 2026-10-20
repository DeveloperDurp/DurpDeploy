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
	t.Helper()
	f := newArtifactE2E(t)
	var step struct {
		ID   int64
		Name string
	}
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Original step", "script_body": `printf 'original-script:%s\n' "$A10_VALUE"`,
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
		"variable_names":  []string{"A10_VALUE"},
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
	f.completion(t, original.ID, events.DeploymentSucceeded)
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
	if !strings.Contains(string(logs), "original-script:base-two") ||
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
	fixture := newRerunInputsFixture(t)
	f := fixture.f
	var next db.Deployment
	decodeStepLogTest(t, f.api(t, "POST", fmt.Sprintf(
		"/api/v1/deployments/%d/redeploy", fixture.original.ID,
	), nil, 201), &next)
	fixture.check(t, next)
}

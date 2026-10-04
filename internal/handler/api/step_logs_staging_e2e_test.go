//go:build e2e

package api_test

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func stagingFailureE2E(t *testing.T) (*artifactE2E, db.Deployment) {
	t.Helper()
	binary := fakePodman(t)
	cli, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	cli = []byte(strings.Replace(string(cli), `case "$3" in`, `case "$3" in
volume) if [ "$4" = create ]; then exit 7; fi;;`, 1))
	if err := os.WriteFile(binary, cli, 0o700); err != nil {
		t.Fatal(err)
	}
	f := newArtifactE2E(t, binary)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "Cannot stage", "script_body": "echo must-not-run",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	var release db.Release
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/releases",
			map[string]string{"version": "staging-failure"},
			201,
		),
		&release,
	)
	var deployment db.Deployment
	decodeStepLogTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/deployments",
			map[string]int64{
				"release_id":     release.ID,
				"environment_id": f.environment.ID,
			},
			201,
		),
		&deployment,
	)
	path := fmt.Sprintf("/api/v1/deployments/%d", deployment.ID)
	logs := f.readStepLogs(t, path+"/logs/stream?format=structured", "")
	if len(logs) != 2 || logs[1].State != "failed" ||
		logs[1].StepIndex == nil ||
		*logs[1].StepIndex != 0 ||
		!strings.Contains(logs[0].Line, "staging failed before execution") {
		t.Fatalf("staging lifecycle: %+v", logs)
	}
	decodeStepLogTest(t, f.api(t, "GET", path, nil, 200), &deployment)
	if deployment.Status != "failed" {
		t.Fatalf("deployment: %+v", deployment)
	}
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/deployments/%d", deployment.ID),
		nil,
		200,
	)
	if !strings.Contains(page, ">Failed<") ||
		strings.Contains(page, "State unavailable") {
		t.Fatal(
			"staging failure omitted the failed step state",
		)
	}
	return f, deployment
}

func TestDeploymentStagingFailureAPIWebE2E(t *testing.T) {
	stagingFailureE2E(t)
}

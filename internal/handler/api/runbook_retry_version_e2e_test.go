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

type runbookRetryVersionFixture struct {
	f        *artifactE2E
	original db.RunbookExecution
	latest   db.RunbookExecution
	logs     []byte
}

func newRunbookRetryVersion(t *testing.T) runbookRetryVersionFixture {
	t.Helper()
	f := newArtifactE2E(t)
	steps := []map[string]string{
		{"name": "Recovery", "script_body": "echo original-version-one; exit 7",
			"container_image": "harbor.durp.info/dockerhub/library/bash:5.2"},
	}
	var saved deletionRunbook
	decodeStepLogTest(t, f.api(t, "POST", f.base()+"/runbooks", map[string]any{
		"name": "Recovery drill <&>", "steps": steps}, 201), &saved)
	path := fmt.Sprintf("%s/runbooks/%d", f.base(), saved.Runbook.ID)
	var original db.RunbookExecution
	body := map[string]int64{"environment_id": f.environment.ID}
	decodeStepLogTest(
		t,
		f.api(t, "POST", path+"/executions", body, 201),
		&original,
	)
	f.completion(t, original.DeploymentID, events.RunbookFailed)
	logs := f.api(
		t,
		"GET",
		fmt.Sprintf("%s/runbook-executions/%d/logs", f.base(), original.ID),
		nil,
		200,
	)
	steps[0]["script_body"] = "echo edited-version-two"
	f.api(t, "PUT", path, map[string]any{"steps": steps}, 201)
	var latest db.RunbookExecution
	decodeStepLogTest(
		t,
		f.api(t, "POST", path+"/executions", body, 201),
		&latest,
	)
	f.completion(t, latest.DeploymentID, events.RunbookSucceeded)
	return runbookRetryVersionFixture{
		f:        f,
		original: original,
		latest:   latest,
		logs:     logs,
	}
}

func (fixture runbookRetryVersionFixture) check(
	t *testing.T,
	retried db.RunbookExecution,
) {
	t.Helper()
	f := fixture.f
	if retried.ID == fixture.original.ID ||
		retried.RunbookVersionID != fixture.original.RunbookVersionID ||
		retried.RunbookVersionID == fixture.latest.RunbookVersionID {
		t.Fatal("Retry did not pin the original runbook version")
	}
	f.completion(t, retried.DeploymentID, events.RunbookFailed)
	var detail db.GetRunbookExecutionRow
	path := fmt.Sprintf("%s/runbook-executions/%d", f.base(), retried.ID)
	decodeStepLogTest(t, f.api(t, "GET", path, nil, 200), &detail)
	logs := f.api(t, "GET", path+"/logs", nil, 200)
	if detail.Version != 1 || detail.EnvironmentID != f.environment.ID ||
		!strings.Contains(
			string(logs),
			"original-version-one",
		) || strings.Contains(string(logs), "edited-version-two") {
		t.Fatal("Retry executed edited content or changed its environment")
	}
	after := f.api(
		t,
		"GET",
		fmt.Sprintf(
			"%s/runbook-executions/%d/logs",
			f.base(),
			fixture.original.ID,
		),
		nil,
		200,
	)
	if !bytes.Equal(fixture.logs, after) {
		t.Fatal("Retry changed the original execution logs")
	}
}

func TestRunbookRetryVersionAPIWebE2E(t *testing.T) {
	fixture := newRunbookRetryVersion(t)
	f := fixture.f
	path := fmt.Sprintf(
		"%s/runbook-executions/%d",
		f.base(),
		fixture.original.ID,
	)
	webPath := fmt.Sprintf(
		"/projects/%d/runbooks/executions/%d",
		f.project.ID,
		fixture.original.ID,
	)
	for _, suffix := range []string{"", "/status"} {
		page := f.web(t, "GET", webPath+suffix, nil, 200)
		if !strings.Contains(page, "saved runbook version 1?") ||
			!strings.Contains(page, "Later runbook edits are not included") {
			t.Fatal("Retry confirmation omits its original version policy")
		}
	}
	var retried db.RunbookExecution
	decodeStepLogTest(t, f.api(t, "POST", path+"/retry", nil, 201), &retried)
	fixture.check(t, retried)
	viewer, userID := newDeletionMember(t, f, "viewer")
	f.api(
		t,
		"POST",
		f.base()+"/members",
		map[string]any{"user_id": userID, "role": "viewer"},
		201,
	)
	viewer.api(t, "POST", path+"/retry", nil, 403)
	page := viewer.web(t, "GET", webPath, nil, 200)
	if strings.Contains(page, `x-data="runbookRetry"`) {
		t.Fatal("Viewer sees a retry control")
	}
}

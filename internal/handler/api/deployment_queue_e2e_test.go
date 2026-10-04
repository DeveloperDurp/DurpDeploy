//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func newQueueE2E(t *testing.T) (*artifactE2E, db.Release, db.Release) {
	t.Helper()
	f := newArtifactE2E(t)
	go f.h.runner.ServeQueue(t.Context())
	var step struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Hold environment", "script_body": "echo queue-head; sleep 120",
		"container_image": "docker.io/library/bash:5.2",
	}, 201), &step); err != nil {
		t.Fatal(err)
	}
	slow := verificationRelease(t, f, "queue-slow")
	f.api(t, "DELETE", fmt.Sprintf("%s/steps/%d", f.base(), step.ID), nil, 204)
	f.api(t, "POST", f.base()+"/steps", map[string]any{
		"name": "Fast work", "script_body": "echo queue-following-work",
		"container_image": "docker.io/library/bash:5.2",
	}, 201)
	return f, slow, verificationRelease(t, f, "queue-fast")
}

func TestEnvironmentQueueAPIWebE2E(t *testing.T) {
	// Given: an executing local deployment and two later API requests.
	f, slow, fast := newQueueE2E(t)
	head := verificationDeploy(t, f, slow)
	waitVerificationLog(t, f,
		fmt.Sprintf("/api/v1/deployments/%d", head.ID), "queue-head")
	cancelled := verificationDeploy(t, f, fast)
	next := verificationDeploy(t, f, fast)
	var status struct {
		Status             string `json:"status"`
		QueuePosition      int64  `json:"queue_position"`
		ActiveDeploymentID int64  `json:"active_deployment_id"`
	}
	if err := json.Unmarshal(f.api(t, "GET",
		fmt.Sprintf("/api/v1/deployments/%d/status", next.ID), nil, 200), &status); err != nil {
		t.Fatal(err)
	}
	if status.Status != "queued" || status.QueuePosition != 2 ||
		status.ActiveDeploymentID != head.ID {
		t.Fatalf("queue status=%+v", status)
	}
	page := f.web(t, "GET", fmt.Sprintf("/deployments/%d", next.ID), nil, 200)
	if !strings.Contains(page, "Queue position: 2") ||
		!strings.Contains(page, fmt.Sprintf("Active work #%d", head.ID)) {
		t.Fatal("web queue state is missing")
	}
	// When: queued work is cancelled in the web UI, then active work through API.
	f.web(
		t,
		"POST",
		fmt.Sprintf("/deployments/%d/cancel", cancelled.ID),
		nil,
		303,
	)
	waitVerificationStatus(t, f, head.ID, "running")
	waitVerificationStatus(t, f, cancelled.ID, "cancelled")
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/cancel", head.ID),
		nil,
		200,
	)
	// Then: only the remaining item executes after the head stops.
	f.completion(t, next.ID, events.DeploymentSucceeded)
	waitVerificationStatus(t, f, head.ID, "cancelled")
	waitVerificationLog(t, f,
		fmt.Sprintf("/api/v1/deployments/%d", next.ID), "queue-following-work")
	logs := f.api(t, "GET",
		fmt.Sprintf("/api/v1/deployments/%d/logs", cancelled.ID), nil, 200)
	if strings.Contains(string(logs), "queue-following-work") {
		t.Fatal("cancelled queued work executed")
	}
}

func TestEnvironmentQueueViewerAndProjectBoundaryE2E(t *testing.T) {
	// Given: a viewer can read their project's queued deployment.
	f, slow, fast := newQueueE2E(t)
	head := verificationDeploy(t, f, slow)
	waitVerificationLog(t, f,
		fmt.Sprintf("/api/v1/deployments/%d", head.ID), "queue-head")
	queued := verificationDeploy(t, f, fast)
	adminToken := f.token
	viewer := seedAPIUser(t, f.h.repo, "queue-viewer@example.test", "viewer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: f.project.ID, UserID: viewer.ID, Role: "deployer",
	}); err != nil {
		t.Fatal(err)
	}
	_, f.token = seedAPIToken(t, f.h.repo, viewer.ID)
	path := fmt.Sprintf("/api/v1/deployments/%d", queued.ID)
	// When: the viewer reads queue state and attempts cancellation.
	result := f.api(t, "GET", path+"/status", nil, 200)
	f.api(t, "POST", path+"/cancel", nil, 403)
	// Then: queue metadata is readable and the write remains blocked.
	if !strings.Contains(string(result), `"queue_position":1`) {
		t.Fatalf("viewer queue status=%s", result)
	}
	outsider := seedAPIUser(
		t,
		f.h.repo,
		"queue-outsider@example.test",
		"deployer",
	)
	_, f.token = seedAPIToken(t, f.h.repo, outsider.ID)
	f.api(t, "GET", path+"/status", nil, 403)
	f.api(t, "POST", path+"/cancel", nil, 403)
	f.token = adminToken
	var privateProject struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(f.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "Other queue project"}, 201), &privateProject); err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: privateProject.ID, UserID: outsider.ID, Role: "deployer",
	}); err != nil {
		t.Fatal(err)
	}
	var privateRelease db.Release
	privateBase := fmt.Sprintf("/api/v1/projects/%d", privateProject.ID)
	if err := json.Unmarshal(f.api(t, "POST", privateBase+"/releases",
		map[string]string{"version": "private-queue"}, 201), &privateRelease); err != nil {
		t.Fatal(err)
	}
	var privateQueue db.Deployment
	if err := json.Unmarshal(f.api(t, "POST", privateBase+"/deployments",
		map[string]int64{"release_id": privateRelease.ID, "environment_id": f.environment.ID}, 201), &privateQueue); err != nil {
		t.Fatal(err)
	}
	_, f.token = seedAPIToken(t, f.h.repo, outsider.ID)
	privatePath := fmt.Sprintf("/api/v1/deployments/%d", privateQueue.ID)
	privateState := f.api(t, "GET", privatePath+"/status", nil, 200)
	if strings.Contains(string(privateState), "active_deployment_id") ||
		strings.Contains(string(privateState), "active_work_url") {
		t.Fatalf("another project's active work leaked: %s", privateState)
	}
	f.api(t, "POST", privatePath+"/cancel", nil, 200)
	f.token = adminToken
	f.api(t, "POST", path+"/cancel", nil, 200)
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/cancel", head.ID),
		nil,
		200,
	)
	waitVerificationStatus(t, f, head.ID, "cancelled")
}

func queueRunbook(t *testing.T, f *artifactE2E) db.Runbook {
	t.Helper()
	var saved struct {
		Runbook db.Runbook `json:"runbook"`
	}
	if err := json.Unmarshal(f.api(t, "POST", f.base()+"/runbooks", map[string]any{
		"name":  "Queued maintenance",
		"steps": []map[string]any{{"name": "Maintenance", "script_body": "echo queue-runbook-work", "container_image": "docker.io/library/bash:5.2"}},
	}, 201), &saved); err != nil {
		t.Fatal(err)
	}
	return saved.Runbook
}

func queueRunbookExecution(
	t *testing.T,
	f *artifactE2E,
	book db.Runbook,
) db.RunbookExecution {
	t.Helper()
	var execution db.RunbookExecution
	if err := json.Unmarshal(f.api(t, "POST", fmt.Sprintf("%s/runbooks/%d/executions", f.base(), book.ID), map[string]int64{
		"environment_id": f.environment.ID,
	}, 201), &execution); err != nil {
		t.Fatal(err)
	}
	return execution
}

func TestEnvironmentQueueRunbookE2E(t *testing.T) {
	// Given: a deployment owns the environment before a runbook request.
	f, slow, _ := newQueueE2E(t)
	head := verificationDeploy(t, f, slow)
	waitVerificationLog(
		t,
		f,
		fmt.Sprintf("/api/v1/deployments/%d", head.ID),
		"queue-head",
	)
	book := queueRunbook(t, f)
	execution := queueRunbookExecution(t, f, book)
	apiPath := fmt.Sprintf("%s/runbook-executions/%d", f.base(), execution.ID)
	webPath := fmt.Sprintf(
		"/projects/%d/runbooks/executions/%d",
		f.project.ID,
		execution.ID,
	)
	result := f.api(t, "GET", apiPath, nil, 200)
	if !strings.Contains(string(result), `"status":"queued"`) ||
		!strings.Contains(string(result), `"queue_position":1`) {
		t.Fatalf("runbook queue state=%s", result)
	}
	if page := f.web(t, "GET", webPath, nil, 200); !strings.Contains(
		page,
		"Queue position: 1",
	) {
		t.Fatal("runbook web queue state missing")
	}
	// When: the queued runbook is cancelled and retried through public endpoints.
	f.web(t, "POST", webPath+"/cancel", nil, 303)
	if state := f.api(t, "GET", apiPath, nil, 200); !strings.Contains(
		string(state),
		`"status":"cancelled"`,
	) {
		t.Fatalf("cancelled runbook=%s", state)
	}
	apiCancelled := queueRunbookExecution(t, f, book)
	cancelPath := fmt.Sprintf("%s/runbook-executions/%d/cancel",
		f.base(), apiCancelled.ID)
	if response := f.api(t, "POST", cancelPath, nil, 200); !strings.Contains(
		string(response), `"status":"cancelled"`,
	) {
		t.Fatalf("queued runbook cancel response=%s", response)
	}
	var retried db.RunbookExecution
	if err := json.Unmarshal(f.api(t, "POST", apiPath+"/retry", nil, 201), &retried); err != nil {
		t.Fatal(err)
	}
	retryPath := fmt.Sprintf("%s/runbook-executions/%d", f.base(), retried.ID)
	if state := f.api(t, "GET", retryPath, nil, 200); !strings.Contains(
		string(state),
		`"status":"queued"`,
	) {
		t.Fatalf("retried runbook=%s", state)
	}
	// Then: the retried runbook executes after the deployment terminates.
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/cancel", head.ID),
		nil,
		200,
	)
	f.completion(t, retried.DeploymentID, events.RunbookSucceeded)
	waitVerificationLog(
		t,
		f,
		retryPath,
		"queue-runbook-work",
	)
}

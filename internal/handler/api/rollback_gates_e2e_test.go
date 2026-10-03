//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestRollbackPermissionsAndApprovalE2E(t *testing.T) {
	// Given: two successful releases in an environment shared with other projects.
	f := newVerificationE2E(t)
	v1 := verificationRelease(t, f, "approval-v1")
	first := verificationDeploy(t, f, v1)
	f.completion(t, first.ID, events.DeploymentSucceeded)
	f.api(
		t,
		"GET",
		fmt.Sprintf("/api/v1/deployments/%d/rollback", first.ID),
		nil,
		409,
	)
	v2 := verificationRelease(t, f, "approval-v2")
	second := verificationDeploy(t, f, v2)
	f.completion(t, second.ID, events.DeploymentSucceeded)
	path := fmt.Sprintf("/api/v1/deployments/%d/rollback", second.ID)
	f.api(
		t,
		"POST",
		path,
		map[string]int64{"target_deployment_id": second.ID},
		409,
	)
	f.api(t, "POST", path, map[string]int64{}, 400)

	// When: an outsider or a viewer tries to inspect or execute rollback.
	adminToken := f.token
	for _, role := range []string{"deployer", "viewer"} {
		user := seedAPIUser(t, f.h.repo, role+"@rollback.example", role)
		_, f.token = seedAPIToken(t, f.h.repo, user.ID)
		f.api(t, "GET", path, nil, 403)
		f.api(
			t,
			"POST",
			path,
			map[string]int64{"target_deployment_id": first.ID},
			403,
		)
	}
	f.token = adminToken

	// And: the operator adds a lifecycle that excludes this environment.
	var lifecycle struct{ ID int64 }
	if err := json.Unmarshal(f.api(
		t,
		"POST",
		"/api/v1/lifecycles",
		map[string]string{
			"name": "Rollback approval",
		},
		201,
	), &lifecycle); err != nil {
		t.Fatal(err)
	}
	f.api(
		t,
		"PUT",
		f.base(),
		map[string]any{"name": f.project.Name, "lifecycle_id": lifecycle.ID},
		200,
	)
	f.api(
		t,
		"POST",
		path,
		map[string]int64{"target_deployment_id": first.ID},
		422,
	)
	webPath := fmt.Sprintf("/deployments/%d/rollback", second.ID)
	blocked := f.web(t, "GET", webPath, nil, 200)
	if !strings.Contains(blocked, "not part of the lifecycle") ||
		strings.Contains(blocked, "target_deployment_id") {
		t.Fatal("blocked rollback exposed a submit form")
	}
	f.web(
		t,
		"POST",
		webPath,
		url.Values{"target_deployment_id": {fmt.Sprint(first.ID)}},
		422,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
		map[string]any{
			"environment_id":    f.environment.ID,
			"sort_order":        1,
			"requires_approval": true,
		},
		201,
	)
	confirmation := f.web(t, "GET", webPath, nil, 200)
	if !strings.Contains(confirmation, "Approval is required") {
		t.Fatal("missing approval notice")
	}

	// Then: rollback waits for the standard admin approval before execution.
	var rollback db.Deployment
	if err := json.Unmarshal(f.api(
		t,
		"POST",
		path,
		map[string]int64{
			"target_deployment_id": first.ID,
		},
		201,
	), &rollback); err != nil {
		t.Fatal(err)
	}
	if rollback.Status != "pending_approval" {
		t.Fatalf("rollback=%+v", rollback)
	}
	f.api(
		t,
		"POST",
		path,
		map[string]int64{"target_deployment_id": first.ID},
		409,
	)
	f.api(t, "POST", fmt.Sprintf("/api/v1/deployments/%d/approve", rollback.ID),
		nil, 200)
	f.completion(t, rollback.ID, events.DeploymentSucceeded)
}

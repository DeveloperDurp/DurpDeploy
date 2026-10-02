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
	"durpdeploy/internal/repository"
)

func TestRollbackAPIWebContainerE2E(t *testing.T) {
	// Given: v1 succeeded with a pinned package and snapshotted variables;
	// v2 failed after project scripts and variables changed.
	f := newArtifactE2E(t)
	var variable struct{ ID int64 }
	if err := json.Unmarshal(f.api(t, "POST", f.base()+"/variables",
		map[string]string{"name": "VERSION_LABEL", "value": "original"}, 201),
		&variable); err != nil {
		t.Fatal(err)
	}
	_, v1 := f.createArtifactRelease(
		t,
		`set -eu; test "$(cat "$ARTIFACT_PATH/app.txt")" = package; test "$VERSION_LABEL" = original; printf 'original-release\n'`,
	)
	good := verificationDeploy(t, f, v1)
	f.completion(t, good.ID, events.DeploymentSucceeded)
	f.api(t, "PUT", fmt.Sprintf("%s/variables/%d", f.base(), variable.ID),
		map[string]string{"name": "VERSION_LABEL", "value": "changed"}, 200)
	steps, err := f.h.repo.Queries.ListStepsByProject(t.Context(), f.project.ID)
	if err != nil || len(steps) != 1 {
		t.Fatalf("steps=%v err=%v", steps, err)
	}
	f.api(
		t,
		"PUT",
		fmt.Sprintf("%s/steps/%d", f.base(), steps[0].ID),
		map[string]any{
			"name":            "Deploy",
			"script_body":     "echo changed-release; exit 23",
			"container_image": "docker.io/library/bash:5.2",
			"sort_order":      1,
		},
		200,
	)
	v2 := verificationRelease(t, f, "failed-v2")
	failed := verificationDeploy(t, f, v2)
	f.completion(t, failed.ID, events.DeploymentFailed)
	path := fmt.Sprintf("/deployments/%d/rollback", failed.ID)
	var preview repository.RollbackPreview
	if err := json.Unmarshal(
		f.api(t, "GET", "/api/v1"+path, nil, 200),
		&preview,
	); err != nil {
		t.Fatal(err)
	}
	if preview.TargetDeploymentID != good.ID ||
		preview.TargetReleaseID != v1.ID {
		t.Fatalf("rollback preview=%+v", preview)
	}
	confirmation := f.web(t, "GET", path, nil, 200)
	if !strings.Contains(confirmation, v1.Version) ||
		!strings.Contains(confirmation, v2.Version) ||
		!strings.Contains(confirmation, f.environment.Name) {
		t.Fatal(
			"rollback confirmation omitted the exact source, target, or environment",
		)
	}
	configureVerification(t, f, "bash", "printf 'rollback-verified\\n'", 5)

	// When: the operator confirms rollback through the web form.
	f.web(t, "POST", path, url.Values{
		"target_deployment_id": {fmt.Sprint(preview.TargetDeploymentID)},
	}, 303)
	var list struct {
		Items []db.Deployment `json:"items"`
	}
	if err := json.Unmarshal(
		f.api(t, "GET", f.base()+"/deployments", nil, 200),
		&list,
	); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) == 0 {
		t.Fatal("rollback created no deployment")
	}
	rolledBack := list.Items[0]
	f.completion(t, rolledBack.ID, events.DeploymentSucceeded)

	// Then: a new audited deployment uses the original script, variables, and pin.
	if rolledBack.ID == good.ID || rolledBack.ID == failed.ID ||
		rolledBack.ReleaseID != v1.ID || rolledBack.EnvironmentID != f.environment.ID {
		t.Fatalf("rollback deployment=%+v", rolledBack)
	}
	logs := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", rolledBack.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(logs, "original-release") ||
		!strings.Contains(
			logs,
			"rollback-verified",
		) || strings.Contains(logs, "changed-release") {
		t.Fatalf(
			"rollback did not use frozen inputs and current verification: %s",
			logs,
		)
	}
	audit := string(
		f.api(
			t,
			"GET",
			"/api/v1/admin/audit?action=rollback_deployment",
			nil,
			200,
		),
	)
	if !strings.Contains(audit, "rollback_deployment") {
		t.Fatalf("rollback audit=%s", audit)
	}
	f.api(t, "POST", "/api/v1"+path,
		map[string]int64{"target_deployment_id": good.ID}, 409)
	f.api(
		t,
		"POST",
		fmt.Sprintf("%s/releases/%d/refresh", f.base(), v1.ID),
		nil,
		409,
	)
}

func TestRollbackAPICreatesDeploymentE2E(t *testing.T) {
	// Given: two successful different releases and no environment verification.
	f := newVerificationE2E(t)
	v1 := verificationRelease(t, f, "api-v1")
	first := verificationDeploy(t, f, v1)
	f.completion(t, first.ID, events.DeploymentSucceeded)
	v2 := verificationRelease(t, f, "api-v2")
	second := verificationDeploy(t, f, v2)
	f.completion(t, second.ID, events.DeploymentSucceeded)

	// When: the API submits the confirmed target.
	var rollback db.Deployment
	if err := json.Unmarshal(f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/rollback", second.ID),
		map[string]int64{
			"target_deployment_id": first.ID,
		},
		201,
	), &rollback); err != nil {
		t.Fatal(err)
	}
	f.completion(t, rollback.ID, events.DeploymentSucceeded)

	// Then: rollback is a distinct execution and verification remains disabled.
	if rollback.ID == second.ID || rollback.ReleaseID != v1.ID {
		t.Fatalf("rollback=%+v", rollback)
	}
	check := string(
		f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/verification", rollback.ID),
			nil,
			200,
		),
	)
	if !strings.Contains(check, `"status":"disabled"`) {
		t.Fatalf("disabled verification=%s", check)
	}
}

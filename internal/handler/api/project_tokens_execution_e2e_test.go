//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestProjectTokensExecutionAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	owner := f.token
	step := map[string]string{"name": "scoped execution",
		"script_body": "printf scoped-execution-ok", "interpreter": "bash",
		"container_image": "docker.io/library/bash:5.2"}
	f.api(t, "POST", f.base()+"/steps", step, 201)
	var release db.Release
	if err := json.Unmarshal(f.api(
		t,
		"POST",
		f.base()+"/releases",
		map[string]string{
			"version": "scoped-execution",
		},
		201,
	), &release); err != nil {
		t.Fatal(err)
	}
	var runbook struct {
		Runbook struct {
			ID int64 `json:"id"`
		} `json:"runbook"`
	}
	if err := json.Unmarshal(
		f.api(t, "POST", f.base()+"/runbooks", map[string]any{
			"name": "scoped diagnostic", "steps": []map[string]string{step},
		}, 201),
		&runbook,
	); err != nil {
		t.Fatal(err)
	}
	for _, role := range []string{"viewer", "deployer", "maintainer", "admin"} {
		f.token = owner
		token := mintProjectToken(t, f, role, f.project.ID)
		f.token = token.ID
		want := 201
		if role == "viewer" {
			want = 403
		}
		data := f.api(t, "POST", f.base()+"/deployments", map[string]any{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, want)
		if role != "viewer" {
			var deployment db.Deployment
			if err := json.Unmarshal(data, &deployment); err != nil {
				t.Fatal(err)
			}
			f.completion(t, deployment.ID, events.DeploymentSucceeded)
			logs := f.api(
				t,
				"GET",
				fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
				nil,
				200,
			)
			if len(logs) == 0 {
				t.Fatal("deployment logs missing")
			}
		}
		if role == "deployer" {
			want = 403
		}
		data = f.api(t, "POST", fmt.Sprintf("%s/runbooks/%d/executions",
			f.base(), runbook.Runbook.ID), map[string]any{
			"environment_id": f.environment.ID,
		}, want)
		if role != "viewer" && role != "deployer" {
			var execution db.RunbookExecution
			if err := json.Unmarshal(data, &execution); err != nil {
				t.Fatal(err)
			}
			f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
			f.api(t, "GET", fmt.Sprintf("%s/runbook-executions/%d/logs",
				f.base(), execution.ID), nil, 200)
			// Backing deployment routes retain their existing runbook boundary.
			f.api(t, "GET", fmt.Sprintf("/api/v1/deployments/%d/logs",
				execution.DeploymentID), nil, 404)
		}
	}
	// Approval remains available only to an administrator-owned admin token.
	f.token = owner
	var lifecycle struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(f.api(
		t,
		"POST",
		"/api/v1/lifecycles",
		map[string]string{
			"name": "token approval",
		},
		201,
	), &lifecycle); err != nil {
		t.Fatal(err)
	}
	f.api(t, "POST", fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID),
		map[string]any{"environment_id": f.environment.ID, "sort_order": 0,
			"requires_approval": true}, 201)
	f.api(t, "PUT", f.base(), map[string]any{"name": f.project.Name,
		"lifecycle_id": lifecycle.ID}, 200)
	token := mintProjectToken(t, f, "admin", f.project.ID)
	f.token = token.ID
	var pending db.Deployment
	if err := json.Unmarshal(f.api(
		t,
		"POST",
		f.base()+"/deployments",
		map[string]any{
			"release_id":     release.ID,
			"environment_id": f.environment.ID,
		},
		201,
	), &pending); err != nil {
		t.Fatal(err)
	}
	if pending.Status != "pending_approval" {
		t.Fatal("approval gate was bypassed")
	}
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/approve", pending.ID),
		nil,
		200,
	)
	f.completion(t, pending.ID, events.DeploymentSucceeded)
}

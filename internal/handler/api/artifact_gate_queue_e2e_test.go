//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/events"
)

func TestArtifactGateEnvironmentQueueE2E(t *testing.T) {
	for _, action := range []string{"approve", "reject", "cancel", "expire", "cancel-approved"} {
		t.Run(action, func(t *testing.T) {
			f, head, gate := newGateDeployment(t)
			steps, err := f.h.repo.Queries.ListStepsByProject(
				t.Context(),
				f.project.ID,
			)
			if err != nil {
				t.Fatal(err)
			}
			for _, step := range steps {
				f.api(
					t,
					"DELETE",
					fmt.Sprintf("%s/steps/%d", f.base(), step.ID),
					nil,
					204,
				)
			}
			f.api(t, "POST", f.base()+"/steps", map[string]string{
				"name":            "Following work",
				"script_body":     "echo queued-after-gate",
				"container_image": "docker.io/library/bash:5.2",
			}, 201)
			next := verificationDeploy(
				t,
				f,
				verificationRelease(t, f, "after-gate"),
			)
			if action != "cancel-approved" {
				go f.h.runner.ServeQueue(t.Context())
			}
			if err := f.h.repo.ReconcileDeploymentQueues(
				t.Context(),
			); err != nil {
				t.Fatal(err)
			}
			path := fmt.Sprintf("/api/v1/deployments/%d", head.ID)
			nextPath := fmt.Sprintf("/api/v1/deployments/%d", next.ID)
			var status struct {
				Status   string `json:"status"`
				ActiveID int64  `json:"active_deployment_id"`
			}
			if err := json.Unmarshal(
				f.api(t, "GET", nextPath+"/status", nil, 200),
				&status,
			); err != nil || status.Status != "queued" ||
				status.ActiveID != head.ID {
				t.Fatalf("queue state=%+v err=%v", status, err)
			}
			page := f.web(
				t,
				"GET",
				fmt.Sprintf("/deployments/%d", next.ID),
				nil,
				200,
			)
			if !strings.Contains(page, "Queue position: 1") {
				t.Fatal("web queue state missing")
			}
			switch action {
			case "approve", "reject":
				f.api(t, "POST", path+"/artifact-gates/0/"+action,
					map[string]any{"revision": 1, "sha256": gate.SHA256}, 200)
				if action == "approve" {
					f.completion(t, head.ID, events.DeploymentSucceeded)
				}
			case "cancel":
				f.api(t, "POST", path+"/cancel", nil, 200)
			case "expire":
				if _, err := f.h.repo.DB.Exec(
					"UPDATE artifact_gates SET expires_at=0 WHERE deployment_id=?",
					head.ID,
				); err != nil {
					t.Fatal(err)
				}
				if err := f.h.repo.MaintainArtifactGates(t.Context()); err != nil {
					t.Fatal(err)
				}
				f.api(t, "GET", path+"/artifact-gates", nil, 200)
			case "cancel-approved":
				// Commit approval without dispatch to exercise prestart cancellation.
				if err := f.h.repo.ApproveArtifact(
					t.Context(),
					head.ID,
					0,
					1,
					1,
					gate.SHA256,
				); err != nil {
					t.Fatal(err)
				}
				f.api(t, "POST", path+"/cancel", nil, 200)
				go f.h.runner.ServeQueue(t.Context())
			}
			f.completion(t, next.ID, events.DeploymentSucceeded)
			waitVerificationLog(t, f, nextPath, "queued-after-gate")
		})
	}
}

//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestLifecycleReorderApprovalAPIWebE2E(t *testing.T) {
	for _, surface := range []string{"web", "API"} {
		t.Run(surface, func(t *testing.T) {
			f := newArtifactE2E(t)
			f.completionTimeout = 2 * time.Minute
			var lifecycle, protected struct{ ID int64 }
			decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
				map[string]string{"name": "Approval reorder"}, 201), &lifecycle)
			decodeLifecycleTest(t, f.api(
				t,
				"POST",
				"/api/v1/environments",
				map[string]string{
					"name": "Protected reorder",
				},
				201,
			), &protected)
			api := fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID)
			var stages [2]db.LifecycleStage
			for i, environment := range []int64{f.environment.ID, protected.ID} {
				decodeLifecycleTest(t, f.api(t, "POST", api, map[string]any{
					"environment_id": environment, "sort_order": 5 + i*5,
					"requires_approval": i == 1,
				}, 201), &stages[i])
			}
			f.api(t, "PUT", f.base(), map[string]any{
				"name": f.project.Name, "lifecycle_id": lifecycle.ID,
			}, 200)
			step := map[string]any{
				"name":            "check",
				"script_body":     "printf 'approval retained\\n'",
				"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
			}
			f.api(t, "POST", f.base()+"/steps", step, 201)
			var release db.Release
			decodeLifecycleTest(t, f.api(
				t,
				"POST",
				f.base()+"/releases",
				map[string]string{
					"version": "reorder-approved",
				},
				201,
			), &release)
			var book struct{ Runbook struct{ ID int64 } }
			decodeLifecycleTest(t, f.api(
				t,
				"POST",
				f.base()+"/runbooks",
				map[string]any{
					"name":  "reorder-check",
					"steps": []any{step},
				},
				201,
			), &book)
			if surface == "web" {
				f.web(
					t,
					"POST",
					fmt.Sprintf("/lifecycles/%d/stages/reorder", lifecycle.ID),
					url.Values{
						"stage_id":  {fmt.Sprint(stages[1].ID)},
						"direction": {"up"},
					},
					303,
				)
			} else {
				f.api(t, "POST", api+"/reorder", map[string]any{
					"stage_ids": []int64{stages[1].ID, stages[0].ID},
				}, 200)
			}
			admin := f.token
			user := seedAPIUser(
				t,
				f.h.repo,
				"reorder-deployer@example.test",
				"deployer",
			)
			if err := f.h.repo.Queries.AddProjectMember(
				t.Context(),
				db.AddProjectMemberParams{
					ProjectID: f.project.ID, UserID: user.ID, Role: "deployer",
				},
			); err != nil {
				t.Fatal(err)
			}
			_, f.token = seedAPIToken(t, f.h.repo, user.ID)
			var deployment db.Deployment
			decodeLifecycleTest(
				t,
				f.api(t, "POST", f.base()+"/deployments", map[string]int64{
					"release_id": release.ID, "environment_id": protected.ID,
				}, 201),
				&deployment,
			)
			if deployment.Status != "pending_approval" {
				t.Fatalf("reordered release bypassed approval: %+v", deployment)
			}
			approve := fmt.Sprintf(
				"/api/v1/deployments/%d/approve",
				deployment.ID,
			)
			f.api(t, "POST", approve, nil, 403)
			f.token = admin
			f.api(t, "POST", approve, nil, 200)
			f.completion(t, deployment.ID, events.DeploymentSucceeded)
			// The runbook API requires Maintainer permission for execution writes.
			maintainer := seedAPIUser(
				t,
				f.h.repo,
				"runbook-reorder@example.test",
				"maintainer",
			)
			if err := f.h.repo.Queries.AddProjectMember(
				t.Context(),
				db.AddProjectMemberParams{
					ProjectID: f.project.ID,
					UserID:    maintainer.ID,
					Role:      "maintainer",
				},
			); err != nil {
				t.Fatal(err)
			}
			_, f.token = seedAPIToken(t, f.h.repo, maintainer.ID)
			var execution db.RunbookExecution
			decodeLifecycleTest(
				t,
				f.api(t, "POST", fmt.Sprintf(
					"%s/runbooks/%d/executions",
					f.base(),
					book.Runbook.ID,
				), map[string]int64{"environment_id": protected.ID}, 201),
				&execution,
			)
			path := fmt.Sprintf(
				"%s/runbook-executions/%d",
				f.base(),
				execution.ID,
			)
			var saved struct{ Status string }
			decodeLifecycleTest(t, f.api(t, "GET", path, nil, 200), &saved)
			if saved.Status != "pending_approval" {
				t.Fatalf(
					"reordered runbook bypassed approval: %s",
					saved.Status,
				)
			}
			f.api(t, "POST", path+"/approve", nil, 403)
			f.token = admin
			f.api(t, "POST", path+"/approve", nil, 200)
			f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
		})
	}
}

//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
)

func TestPermissionRolesExecutionAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "approved command", "script_body": "printf approved-role-execution; exit 1",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "approved-execution")
	user := seedAPIUser(
		t,
		f.h.repo,
		"execution-deployer@roles.test",
		"deployer",
	)
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: f.project.ID, UserID: user.ID, Role: "deployer",
	}); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"global", "scoped"} {
		id, token := seedAPIToken(t, f.h.repo, user.ID)
		grant := repository.TokenScope{Scope: scope, Role: "deployer"}
		if scope == "scoped" {
			grant.ProjectIDs = []int64{f.project.ID}
		}
		if _, err := f.h.repo.ReplaceTokenScope(t.Context(), id, grant); err != nil {
			t.Fatal(err)
		}
		f.token = token
		var original db.Deployment
		if err := json.Unmarshal(f.api(t, "POST", f.base()+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 201), &original); err != nil {
			t.Fatal(err)
		}
		f.completion(t, original.ID, events.DeploymentFailed)
		for _, action := range []string{"retry", "redeploy"} {
			var rerun db.Deployment
			if err := json.Unmarshal(f.api(t, "POST", fmt.Sprintf("/api/v1/deployments/%d/%s",
				original.ID, action), nil, 201), &rerun); err != nil {
				t.Fatal(err)
			}
			f.completion(t, rerun.ID, events.DeploymentFailed)
			originalSource, err := f.h.repo.Queries.GetDeploymentStepSource(
				t.Context(),
				original.ID,
			)
			if err != nil {
				t.Fatal(err)
			}
			rerunSource, err := f.h.repo.Queries.GetDeploymentStepSource(
				t.Context(),
				rerun.ID,
			)
			if err != nil {
				t.Fatal(err)
			}
			if rerun.ID == original.ID ||
				rerunSource.StepsJson != originalSource.StepsJson {
				t.Fatal("retry changed frozen steps or reused execution")
			}
		}
		pending := seedDeployment(
			t,
			f.h.repo,
			release.ID,
			f.environment.ID,
			"pending_approval",
		)
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/approve", pending.ID),
			nil,
			403,
		)
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/rollback", original.ID),
			map[string]int64{"target_deployment_id": original.ID},
			403,
		)
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/cancel", pending.ID),
			nil,
			200,
		)
		current, err := f.h.repo.Queries.GetDeployment(t.Context(), pending.ID)
		if err != nil || current.Status != "cancelled" {
			t.Fatalf("cancel: %+v, %v", current, err)
		}
		// A live membership downgrade restricts the existing bearer immediately.
		if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: user.ID, Role: "viewer",
		}); err != nil {
			t.Fatal(err)
		}
		f.api(t, "POST", f.base()+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 403)
		if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
			ProjectID: f.project.ID, UserID: user.ID, Role: "deployer",
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.h.repo.Queries.UpdateUser(t.Context(), db.UpdateUserParams{
			ID: user.ID, Name: user.Name, Role: "viewer",
		}); err != nil {
			t.Fatal(err)
		}
		f.api(t, "POST", f.base()+"/deployments", map[string]int64{
			"release_id": release.ID, "environment_id": f.environment.ID,
		}, 403)
		if err := f.h.repo.Queries.UpdateUser(t.Context(), db.UpdateUserParams{
			ID: user.ID, Name: user.Name, Role: "deployer",
		}); err != nil {
			t.Fatal(err)
		}
	}
}

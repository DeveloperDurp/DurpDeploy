//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestPermissionRolesExecutionRetryGatesAPIE2E(t *testing.T) {
	f := newArtifactE2E(t)
	ownerToken := f.token
	f.api(t, "POST", f.base()+"/steps", map[string]string{
		"name": "requires approval", "script_body": "echo approved",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}, 201)
	release := verificationRelease(t, f, "retry-approval")
	var lifecycle struct {
		ID int64 `json:"id"`
	}
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/lifecycles",
		map[string]string{"name": "retry gates"}, 201), &lifecycle)
	stagesPath := fmt.Sprintf("/api/v1/lifecycles/%d/stages", lifecycle.ID)
	f.api(t, "POST", stagesPath, map[string]any{
		"environment_id": f.environment.ID, "sort_order": 10,
		"requires_approval": true,
	}, 201)
	f.api(t, "PUT", f.base(), map[string]any{
		"name": f.project.Name, "lifecycle_id": lifecycle.ID,
	}, 200)
	user := seedAPIUser(t, f.h.repo, "retry-deployer@roles.test", "deployer")
	if err := f.h.repo.Queries.AddProjectMember(t.Context(), db.AddProjectMemberParams{
		ProjectID: f.project.ID, UserID: user.ID, Role: "deployer",
	}); err != nil {
		t.Fatal(err)
	}
	var sourceID int64
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
		decodeLifecycleTest(t, f.api(t, "POST", f.base()+"/deployments",
			map[string]int64{"release_id": release.ID,
				"environment_id": f.environment.ID}, 201), &original)
		if original.Status != "pending_approval" {
			t.Fatalf("initial deployment bypassed approval: %+v", original)
		}
		sourceID = original.ID
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/cancel", sourceID),
			nil,
			200,
		)
		var retried db.Deployment
		decodeLifecycleTest(t, f.api(
			t,
			"POST",
			fmt.Sprintf(
				"/api/v1/deployments/%d/retry",
				sourceID,
			),
			nil,
			201,
		), &retried)
		stored, err := f.h.repo.Queries.GetDeployment(t.Context(), retried.ID)
		if err != nil || stored.Status != "pending_approval" ||
			stored.StartedAt.Valid {
			t.Fatalf("%s retry bypassed approval: %+v, %v", scope, stored, err)
		}
		logs := f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", retried.ID),
			nil,
			200,
		)
		var entries []json.RawMessage
		if err := json.Unmarshal(logs, &entries); err != nil ||
			len(entries) != 0 {
			t.Fatalf("unapproved retry produced logs: %s, %v", logs, err)
		}
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/approve", retried.ID),
			nil,
			403,
		)
		f.api(
			t,
			"POST",
			fmt.Sprintf("/api/v1/deployments/%d/cancel", retried.ID),
			nil,
			200,
		)
	}
	// A changed promotion gate also applies to an existing cancelled source.
	deployerToken := f.token
	f.token = ownerToken
	var previous struct {
		ID int64 `json:"id"`
	}
	decodeLifecycleTest(t, f.api(t, "POST", "/api/v1/environments",
		map[string]string{"name": "required previous stage"}, 201), &previous)
	f.api(t, "POST", stagesPath, map[string]any{
		"environment_id": previous.ID, "sort_order": 1,
	}, 201)
	f.token = deployerToken
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/deployments/%d/retry", sourceID),
		nil,
		422,
	)
}

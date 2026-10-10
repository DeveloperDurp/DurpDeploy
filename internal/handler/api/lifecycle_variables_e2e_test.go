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

func TestLifecycleVariablesAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	var lc struct {
		ID int64 `json:"id"`
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			"/api/v1/lifecycles",
			map[string]string{"name": "Shared pipeline"},
			201,
		),
		&lc,
	)
	f.api(
		t,
		"POST",
		fmt.Sprintf("/api/v1/lifecycles/%d/stages", lc.ID),
		map[string]int64{"environment_id": f.environment.ID},
		201,
	)
	path := fmt.Sprintf("/api/v1/lifecycles/%d/variables", lc.ID)
	f.api(
		t,
		"POST",
		path,
		map[string]any{"name": "REGION", "value": "global"},
		201,
	)
	var shared struct {
		ID int64 `json:"id"`
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			path,
			map[string]any{
				"name":           "REGION",
				"value":          "shared-env",
				"environment_id": f.environment.ID,
			},
			201,
		),
		&shared,
	)
	f.api(
		t,
		"POST",
		path,
		map[string]any{
			"name":   "TOKEN",
			"value":  "lifecycle-secret-sentinel",
			"secret": true,
		},
		201,
	)
	for _, project := range []db.Project{f.project, seedProject(t, f.h.repo)} {
		base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
		f.api(
			t,
			"PUT",
			base,
			map[string]any{"name": project.Name, "lifecycle_id": lc.ID},
			200,
		)
		f.api(
			t,
			"POST",
			base+"/steps",
			map[string]any{
				"name":            "shared",
				"script_body":     `test "$REGION" = edited-env; test "$TOKEN" = lifecycle-secret-sentinel; printf '%s %s\n' "$REGION" "$TOKEN"`,
				"container_image": "docker.io/library/bash:5.2",
			},
			201,
		)
		var release db.Release
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				base+"/releases",
				map[string]string{"version": "shared-first"},
				201,
			),
			&release,
		)
		// Shared edits apply to the next deployment without a release refresh.
		f.api(
			t,
			"PUT",
			fmt.Sprintf("%s/%d", path, shared.ID),
			map[string]any{
				"name":           "REGION",
				"value":          "edited-env",
				"environment_id": f.environment.ID,
			},
			200,
		)
		var deployment db.Deployment
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				base+"/deployments",
				map[string]int64{
					"release_id":     release.ID,
					"environment_id": f.environment.ID,
				},
				201,
			),
			&deployment,
		)
		f.completion(t, deployment.ID, events.DeploymentSucceeded)
		logs := f.api(
			t,
			"GET",
			fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID),
			nil,
			200,
		)
		if !strings.Contains(string(logs), "edited-env") ||
			strings.Contains(string(logs), "lifecycle-secret-sentinel") {
			t.Fatal(
				"shared variables not resolved or secret leaked",
				string(logs),
			)
		}
		f.api(
			t,
			"POST",
			fmt.Sprintf("%s/releases/%d/refresh", base, release.ID),
			nil,
			200,
		)
		variables, err := f.h.repo.ListReleaseVariablesByRelease(
			t.Context(),
			release.ID,
		)
		if err != nil || len(variables) != 0 {
			t.Fatal("shared variables leaked into release snapshot", err)
		}
		f.api(
			t,
			"PUT",
			fmt.Sprintf("%s/%d", path, shared.ID),
			map[string]any{
				"name":           "REGION",
				"value":          "shared-env",
				"environment_id": f.environment.ID,
			},
			200,
		)
	}
	webPath := fmt.Sprintf("/lifecycles/%d/variables", lc.ID)
	f.web(
		t,
		"POST",
		webPath,
		url.Values{"name": {"WEB_VALUE"}, "value": {"from-web"}},
		303,
	)
	f.web(
		t,
		"POST",
		webPath,
		url.Values{"name": {"WEB_VALUE"}, "value": {"duplicate"}},
		422,
	)
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/projects/%d/variables", f.project.ID),
		nil,
		200,
	)
	if !strings.Contains(page, "Inherited variables") ||
		!strings.Contains(page, "from-web") ||
		strings.Contains(page, "lifecycle-secret-sentinel") {
		t.Fatal("project inheritance UI missing or exposes secret")
	}
	assertLifecycleRunbookSnapshots(t, f, lc.ID)
	assertLifecycleRetainedScopeAndDuplicateReset(t, f, lc.ID)
	assertLifecycleAssignmentAccess(t, f, lc.ID)
}

func decodeLifecycleTest(t *testing.T, data []byte, target any) {
	t.Helper()
	if err := json.Unmarshal(data, target); err != nil {
		t.Fatal(err)
	}
}

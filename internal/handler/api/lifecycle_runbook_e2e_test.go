//go:build e2e

package api_test

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func assertLifecycleRunbookSnapshots(
	t *testing.T,
	f *artifactE2E,
	lifecycleID int64,
) {
	t.Helper()
	sharedPath := fmt.Sprintf("/api/v1/lifecycles/%d/variables", lifecycleID)
	var shared []struct {
		ID            int64  `json:"id"`
		Name          string `json:"name"`
		EnvironmentID *int64 `json:"environment_id"`
	}
	decodeLifecycleTest(t, f.api(t, "GET", sharedPath, nil, 200), &shared)
	var regionID, secretID int64
	for _, variable := range shared {
		if variable.Name == "REGION" && variable.EnvironmentID != nil {
			regionID = variable.ID
		}
		if variable.Name == "TOKEN" {
			secretID = variable.ID
		}
	}
	if regionID == 0 || secretID == 0 {
		t.Fatal("shared runbook fixture missing")
	}
	script := `case "$REGION" in
shared-env) test "$TOKEN" = lifecycle-secret-sentinel ;;
runbook-api) test "$TOKEN" = runbook-api-secret ;;
runbook-web) test "$TOKEN" = runbook-web-secret ;;
*) exit 1 ;;
esac
printf 'snapshot=%s secret=%s\n' "$REGION" "$TOKEN"`
	steps := []map[string]any{
		{
			"name":            "shared",
			"script_body":     script,
			"container_image": "docker.io/library/bash:5.2",
		},
	}
	var first, second struct {
		Runbook db.Runbook        `json:"runbook"`
		Version db.RunbookVersion `json:"version"`
	}
	decodeLifecycleTest(
		t,
		f.api(
			t,
			"POST",
			f.base()+"/runbooks",
			map[string]any{"name": "inherited-maintenance", "steps": steps},
			201,
		),
		&first,
	)
	path := fmt.Sprintf("%s/runbooks/%d", f.base(), first.Runbook.ID)
	updateShared := func(region, token string) {
		t.Helper()
		f.api(
			t,
			"PUT",
			fmt.Sprintf("%s/%d", sharedPath, regionID),
			map[string]any{
				"name":           "REGION",
				"value":          region,
				"environment_id": f.environment.ID,
			},
			200,
		)
		f.api(
			t,
			"PUT",
			fmt.Sprintf("%s/%d", sharedPath, secretID),
			map[string]any{"name": "TOKEN", "value": token, "secret": true},
			200,
		)
	}
	updateShared("runbook-api", "runbook-api-secret")
	decodeLifecycleTest(
		t,
		f.api(t, "PUT", path, map[string]any{"steps": steps}, 201),
		&second,
	)
	updateShared("runbook-web", "runbook-web-secret")
	f.web(
		t,
		"POST",
		fmt.Sprintf(
			"/projects/%d/runbooks/%d/save",
			f.project.ID,
			first.Runbook.ID,
		),
		url.Values{
			"step_name": {"shared"}, "step_script": {script},
			"step_interpreter": {"bash"}, "step_target": {"local"},
			"step_image":   {"docker.io/library/bash:5.2"},
			"step_timeout": {"0"}, "step_retries": {"0"},
			"step_selectors": {""}, "step_variable_names": {""},
		},
		303,
	)
	var detail struct {
		Versions []db.RunbookVersion `json:"versions"`
	}
	decodeLifecycleTest(t, f.api(t, "GET", path, nil, 200), &detail)
	if len(detail.Versions) != 3 {
		t.Fatalf("runbook versions: %d", len(detail.Versions))
	}
	var webVersionID int64
	for _, version := range detail.Versions {
		if version.ID != first.Version.ID && version.ID != second.Version.ID {
			webVersionID = version.ID
		}
	}
	for _, snapshot := range []struct {
		id    int64
		value string
	}{
		{first.Version.ID, "shared-env"},
		{second.Version.ID, "runbook-api"},
		{webVersionID, "runbook-web"},
	} {
		var execution db.RunbookExecution
		decodeLifecycleTest(
			t,
			f.api(
				t,
				"POST",
				path+"/executions",
				map[string]int64{
					"version_id":     snapshot.id,
					"environment_id": f.environment.ID,
				},
				201,
			),
			&execution,
		)
		f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
		logs := string(
			f.api(
				t,
				"GET",
				fmt.Sprintf(
					"%s/runbook-executions/%d/logs",
					f.base(),
					execution.ID,
				),
				nil,
				200,
			),
		)
		if !strings.Contains(logs, "snapshot="+snapshot.value) ||
			strings.Contains(logs, "lifecycle-secret-sentinel") ||
			strings.Contains(logs, "runbook-api-secret") ||
			strings.Contains(logs, "runbook-web-secret") {
			t.Fatalf(
				"runbook version %d snapshot or redaction mismatch: %s",
				snapshot.id,
				logs,
			)
		}
	}
}

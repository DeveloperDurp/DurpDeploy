//go:build e2e

package api_test

import (
	"fmt"
	"html"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func runbookHistoryPhoneFixture(
	t *testing.T,
) (*artifactE2E, []db.ListRunbookExecutionsRow) {
	t.Helper()
	f := newArtifactE2E(t)
	steps := map[string]any{"steps": []map[string]string{{
		"name": "Recovery check", "script_body": "echo recovery-check; exit 7",
		"container_image": "harbor.durp.info/dockerhub/library/bash:5.2",
	}}}
	steps["name"] = strings.Repeat("Recovery drill ", 5) + "<&>"
	var saved deletionRunbook
	decodeStepLogTest(
		t,
		f.api(t, "POST", f.base()+"/runbooks", steps, 201),
		&saved,
	)
	path := fmt.Sprintf("%s/runbooks/%d", f.base(), saved.Runbook.ID)
	var first db.RunbookExecution
	decodeStepLogTest(t, f.api(t, "POST", path+"/executions",
		map[string]int64{"environment_id": f.environment.ID}, 201), &first)
	f.completion(t, first.DeploymentID, events.RunbookFailed)
	var retry db.RunbookExecution
	decodeStepLogTest(t, f.api(t, "POST", fmt.Sprintf(
		"%s/runbook-executions/%d/retry",
		f.base(),
		first.ID,
	), nil, 201), &retry)
	f.completion(t, retry.DeploymentID, events.RunbookFailed)
	f.api(t, "PUT", path, steps, 201)
	var environment db.Environment
	decodeStepLogTest(t, f.api(
		t,
		"POST",
		"/api/v1/environments",
		map[string]string{
			"name": strings.Repeat("RecoveryEnvironment", 4) + "<&>",
		},
		201,
	), &environment)
	var latest db.RunbookExecution
	decodeStepLogTest(t, f.api(t, "POST", path+"/executions",
		map[string]int64{"environment_id": environment.ID}, 201), &latest)
	f.completion(t, latest.DeploymentID, events.RunbookFailed)
	var page struct {
		Items []db.ListRunbookExecutionsRow `json:"items"`
	}
	decodeStepLogTest(
		t,
		f.api(t, "GET", f.base()+"/runbook-executions", nil, 200),
		&page,
	)
	if len(page.Items) != 3 || page.Items[0].ID != latest.ID ||
		page.Items[1].ID != retry.ID || page.Items[2].ID != first.ID ||
		page.Items[0].Version != 2 || page.Items[1].Version != 1 ||
		page.Items[2].Version != 1 || page.Items[0].EnvironmentID != environment.ID ||
		page.Items[1].EnvironmentID != f.environment.ID {
		t.Fatalf(
			"history lost versions, attempts, or environments: %+v",
			page.Items,
		)
	}
	return f, page.Items
}

func TestRunbookHistoryPhoneAPIWebE2E(t *testing.T) {
	f, rows := runbookHistoryPhoneFixture(t)
	viewer, userID := newDeletionMember(t, f, "viewer")
	list := f.base() + "/runbook-executions"
	web := fmt.Sprintf("/projects/%d/runbooks", f.project.ID)
	viewer.api(t, "GET", list, nil, 403)
	viewer.web(t, "GET", web, nil, 403)
	f.api(t, "POST", f.base()+"/members", map[string]any{
		"user_id": userID, "role": "viewer",
	}, 201)
	for _, actor := range []*artifactE2E{f, &viewer} {
		var page struct {
			Total int64                         `json:"total"`
			Items []db.ListRunbookExecutionsRow `json:"items"`
		}
		decodeStepLogTest(
			t,
			actor.api(t, "GET", list+"?limit=1&offset=1", nil, 200),
			&page,
		)
		if page.Total != 3 || len(page.Items) != 1 || page.Items[0] != rows[1] {
			t.Fatalf("API pagination changed execution identity: %+v", page)
		}
		body := actor.web(t, "GET", web, nil, 200)
		for _, row := range rows {
			if row.CreatedAt <= 0 || row.Status != "failed" ||
				!strings.Contains(
					body,
					fmt.Sprintf(`data-runbook-execution-id="%d"`, row.ID),
				) ||
				!strings.Contains(
					body,
					fmt.Sprintf(
						"#%d — %s",
						row.ID,
						html.EscapeString(row.RunbookName),
					),
				) {
				t.Fatalf("history row identity missing: %+v", row)
			}
		}
	}
	body := viewer.web(t, "GET", web, nil, 200)
	if strings.Contains(body, "New runbook") {
		t.Fatal("Viewer history exposes runbook creation")
	}
	if body = f.web(t, "GET", web+"?offset=3", nil, 200); !strings.Contains(
		body,
		"No runbook executions yet.",
	) {
		t.Fatal("empty history pagination regressed")
	}
}

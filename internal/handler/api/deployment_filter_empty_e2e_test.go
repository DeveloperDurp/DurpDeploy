//go:build e2e

package api_test

import (
	"fmt"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestDeploymentFilterEmptyAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t)
	assertEmptyDeploymentList(t, f, "", "",
		"No deployments yet. Deploy a release to see results here.")

	release := seedRelease(t, f.h.repo, f.project.ID)
	deployment := seedDeployment(t, f.h.repo, release.ID,
		f.environment.ID, "succeeded")
	var emptyProject struct{ ID int64 }
	decodeStepLogTest(t, f.api(t, "POST", "/api/v1/projects",
		map[string]string{"name": "No history"}, 201), &emptyProject)

	for _, query := range []struct{ web, api string }{
		{"status=failed", "status=failed"},
		{"env_id=999999", "env_id=999999"},
		{"from=2099-01-01", "from=4070908800"},
		{"to=2000-01-01", "to=946771199"},
		{fmt.Sprintf("project_id=%d", emptyProject.ID),
			fmt.Sprintf("project_id=%d", emptyProject.ID)},
		{fmt.Sprintf("project_id=%d&env_id=%d&status=failed",
			f.project.ID, f.environment.ID),
			fmt.Sprintf("project_id=%d&env_id=%d&status=failed",
				f.project.ID, f.environment.ID)},
	} {
		t.Run(query.web, func(t *testing.T) {
			assertEmptyDeploymentList(t, f, "?"+query.web, "?"+query.api,
				"No deployments match these filters")
		})
	}

	// Clearing filters recovers the same history without changing it.
	var page struct {
		Items []db.Deployment `json:"items"`
		Total int64           `json:"total"`
	}
	decodeStepLogTest(t, f.api(t, "GET", "/api/v1/deployments",
		nil, 200), &page)
	if page.Total != 1 || len(page.Items) != 1 ||
		page.Items[0].ID != deployment.ID ||
		page.Items[0].Status != "succeeded" {
		t.Fatal("clearing filters changed deployment history")
	}
	pageHTML := f.web(t, "GET", "/deployments", nil, 200)
	if !strings.Contains(pageHTML,
		fmt.Sprintf(`data-deployment-id="%d"`, deployment.ID)) ||
		strings.Contains(pageHTML, "No deployments") {
		t.Fatal("clearing filters did not restore deployment history")
	}
	offsetHTML := f.web(t, "GET", "/deployments?offset=100", nil, 200)
	if strings.Contains(offsetHTML, "No deployments yet.") ||
		!strings.Contains(offsetHTML, "No deployments match these filters") {
		t.Fatal("an exhausted page incorrectly claims there is no history")
	}
	// Other projects' history must not change an empty account's onboarding.
	viewer, _ := newDeletionMember(t, f, "viewer")
	assertEmptyDeploymentList(t, &viewer, "", "",
		"No deployments yet. Deploy a release to see results here.")
}

func assertEmptyDeploymentList(
	t *testing.T, f *artifactE2E, webQuery, apiQuery, message string,
) {
	t.Helper()
	var page struct {
		Items []db.Deployment `json:"items"`
		Total int64           `json:"total"`
	}
	decodeStepLogTest(t, f.api(t, "GET", "/api/v1/deployments"+apiQuery,
		nil, 200), &page)
	if page.Total != 0 || len(page.Items) != 0 {
		t.Fatalf("empty filter returned deployments: %+v", page)
	}
	body := f.web(t, "GET", "/deployments"+webQuery, nil, 200)
	if !strings.Contains(body, message) {
		t.Fatalf("empty state does not explain its result: %s", body)
	}
	_, content, _ := strings.Cut(body, `id="deployments-content"`)
	if webQuery != "" && (strings.Contains(body, "No deployments yet.") ||
		!strings.Contains(content, `href="/deployments"`) ||
		!strings.Contains(content, `>Clear</a>`)) {
		t.Fatal("unmatched filters show onboarding or lack a nearby Clear link")
	}
}

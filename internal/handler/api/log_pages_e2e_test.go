//go:build e2e

package api_test

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestDeploymentLogHistoryPagesAPIWebE2E(t *testing.T) {
	// Given: an active quiet step followed by more than one page of output.
	f := newArtifactE2E(t)
	release, err := f.h.repo.Queries.CreateRelease(t.Context(),
		db.CreateReleaseParams{ProjectID: f.project.ID, Version: "log-pages",
			StepsJson: `[{"name":"Quiet"},{"name":"Noisy"}]`})
	if err != nil {
		t.Fatal(err)
	}
	deployment := seedDeployment(
		t,
		f.h.repo,
		release.ID,
		f.environment.ID,
		"running",
	)
	if _, err := f.h.repo.Queries.CreateStepDeploymentLog(t.Context(),
		db.CreateStepDeploymentLogParams{DeploymentID: deployment.ID,
			StepIndex: sql.NullInt64{Int64: 0, Valid: true},
			StepName:  sql.NullString{String: "Quiet", Valid: true},
			StepState: sql.NullString{String: "running", Valid: true},
			Line:      "quiet-start"}); err != nil {
		t.Fatal(err)
	}
	seedLogPageRows(t, f, deployment.ID)
	apiPath := fmt.Sprintf("/api/v1/deployments/%d/logs", deployment.ID)
	webPath := fmt.Sprintf("/deployments/%d", deployment.ID)
	// When: API and browser users open the latest page and its older history.
	assertLogPages(t, f, apiPath, webPath)
	// Then: the quiet step keeps its independent running state.
	page := f.web(t, "GET", webPath, nil, 200)
	if strings.Contains(page, "quiet-start") ||
		!strings.Contains(page, "Running: Step 1") {
		t.Fatal(
			"quiet step state was lost or old output escaped the bounded page",
		)
	}
	if export := f.web(
		t,
		"GET",
		webPath+"/logs.txt",
		nil,
		200,
	); !strings.Contains(
		export,
		"quiet-start",
	) {
		t.Fatal("full export lost older quiet-step output")
	}
	// Later state changes remain visible when reading an older output page.
	terminal, err := f.h.repo.Queries.CreateStepDeploymentLog(t.Context(),
		db.CreateStepDeploymentLogParams{DeploymentID: deployment.ID,
			StepIndex: sql.NullInt64{Int64: 0, Valid: true},
			StepName:  sql.NullString{String: "Quiet", Valid: true},
			StepState: sql.NullString{String: "succeeded", Valid: true},
			Line:      "quiet-finished"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.h.repo.Queries.UpdateDeploymentStatus(t.Context(),
		db.UpdateDeploymentStatusParams{
			ID: deployment.ID, Status: "succeeded",
		}); err != nil {
		t.Fatal(err)
	}
	historical := f.web(t, "GET",
		fmt.Sprintf("%s?before=%d", webPath, terminal.ID), nil, 200)
	if strings.Contains(historical, "quiet-finished") ||
		!strings.Contains(historical, ">Succeeded</span>") {
		t.Fatal("historical output lost the current step state")
	}
}

func TestRunbookLogHistoryPagesAPIWebE2E(t *testing.T) {
	// Given: a retained runbook execution with multiple pages of history.
	f := newArtifactE2E(t)
	book, _, err := f.h.repo.SaveRunbook(t.Context(), repository.RunbookSave{
		ProjectID: f.project.ID, Name: "log-pages", StepsJSON: "[]",
	})
	if err != nil {
		t.Fatal(err)
	}
	execution, result, err := f.h.repo.CreateRunbookExecution(t.Context(),
		repository.RunbookExecutionRequest{ProjectID: f.project.ID,
			RunbookID: book.ID, EnvironmentID: f.environment.ID})
	if err != nil {
		t.Fatal(err)
	}
	seedLogPageRows(t, f, result.Deployment.ID)
	apiPath := fmt.Sprintf(
		"%s/runbook-executions/%d/logs",
		f.base(),
		execution.ID,
	)
	webPath := fmt.Sprintf(
		"/projects/%d/runbooks/executions/%d",
		f.project.ID,
		execution.ID,
	)
	// When: the same public pagination and web navigation are used.
	assertLogPages(t, f, apiPath, webPath)
	// Then: both API and browser export still return complete history.
	for _, body := range []string{
		string(f.api(t, "GET", strings.TrimSuffix(apiPath, "/logs")+"/logs.txt", nil, 200)),
		f.web(t, "GET", webPath+"/logs.txt", nil, 200),
	} {
		if !strings.Contains(body, "history-0000") ||
			!strings.Contains(body, "history-0509") {
			t.Fatal("runbook export omitted older or latest output")
		}
	}
}

func seedLogPageRows(t *testing.T, f *artifactE2E, deploymentID int64) {
	t.Helper()
	if err := f.h.repo.WithTx(t.Context(), func(q *db.Queries) error {
		for index := range 510 {
			if _, err := q.CreateStepDeploymentLog(t.Context(),
				db.CreateStepDeploymentLogParams{
					DeploymentID: deploymentID,
					StepIndex:    sql.NullInt64{Int64: 1, Valid: true},
					StepName:     sql.NullString{String: "Noisy", Valid: true},
					Line: fmt.Sprintf(
						"history-%04d",
						index,
					),
				}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func assertLogPages(t *testing.T, f *artifactE2E, apiPath, webPath string) {
	t.Helper()
	var page repository.LogPage
	decodeStepLogTest(t, f.api(t, "GET", apiPath+"/page", nil, 200), &page)
	if len(page.Items) != 500 || page.NextBefore != page.Items[0].ID ||
		page.LastID != page.Items[499].ID || page.Items[0].Line != "history-0010" {
		t.Fatal("latest log page has invalid boundaries")
	}
	for i := 1; i < len(page.Items); i++ {
		if page.Items[i-1].ID >= page.Items[i].ID {
			t.Fatal("log page IDs are not strictly increasing")
		}
	}
	var older repository.LogPage
	query := fmt.Sprintf("?before=%d", page.NextBefore)
	decodeStepLogTest(
		t,
		f.api(t, "GET", apiPath+"/page"+query, nil, 200),
		&older,
	)
	if older.NextBefore != 0 || older.LastID >= page.Items[0].ID ||
		len(older.Items) < 10 {
		t.Fatal("older page lost rows or overlaps the latest page")
	}
	var legacy []db.DeploymentLog
	decodeStepLogTest(t, f.api(t, "GET", apiPath, nil, 200), &legacy)
	if len(legacy) != len(page.Items)+len(older.Items) {
		t.Fatal("legacy full-log response was truncated")
	}
	for _, invalid := range []string{"-1", "bad", "0", "9223372036854775808"} {
		f.api(t, "GET", apiPath+"/page?before="+invalid, nil, 400)
		f.web(t, "GET", webPath+"?before="+invalid, nil, 400)
	}
	latestHTML := f.web(t, "GET", webPath, nil, 200)
	if strings.Contains(latestHTML, "history-0000") ||
		strings.Count(latestHTML, " data-log-id=") != 500 {
		t.Fatal("web view did not bound initial history")
	}
	olderHTML := f.web(t, "GET", webPath+query, nil, 200)
	if !strings.Contains(olderHTML, "history-0000") ||
		strings.Contains(olderHTML, "history-0509") {
		t.Fatal("web history navigation omitted or mixed pages")
	}
	assertLogPageAccess(t, f, apiPath, webPath)
}

func assertLogPageAccess(
	t *testing.T,
	f *artifactE2E,
	apiPath, webPath string,
) {
	t.Helper()
	viewer := *f
	user := seedAPIUser(t, f.h.repo, "history-viewer@example.test", "viewer")
	_, viewer.token = seedAPIToken(t, f.h.repo, user.ID)
	var err error
	viewer.session, viewer.csrf, err = auth.NewSessionToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.h.repo.Queries.CreateSession(t.Context(),
		db.CreateSessionParams{
			ID:        viewer.session,
			UserID:    user.ID,
			CsrfToken: viewer.csrf,
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		}); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []int{403, 200, 403} {
		viewer.api(t, "GET", apiPath+"/page", nil, expected)
		viewer.api(t, "GET", apiPath+".txt", nil, expected)
		viewer.web(t, "GET", webPath, nil, expected)
		viewer.web(t, "GET", webPath+"/logs.txt", nil, expected)
		if expected == 200 {
			f.api(
				t,
				"DELETE",
				fmt.Sprintf("%s/members/%d", f.base(), user.ID),
				nil,
				204,
			)
		} else {
			f.api(t, "POST", f.base()+"/members", map[string]any{
				"user_id": user.ID, "role": "deployer",
			}, 201)
		}
	}
}

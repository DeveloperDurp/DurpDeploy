//go:build e2e

package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
)

func TestRunbookDeletionAPIWebE2E(t *testing.T) {
	for _, transport := range []string{"api", "web"} {
		t.Run(transport, func(t *testing.T) {
			checkRunbookDeletionHistory(t, transport)
		})
	}
}

func checkRunbookDeletionHistory(t *testing.T, transport string) {
	t.Helper()
	// Given: a completed execution, two versions, a package pin, and a schedule.
	f := newArtifactE2E(t)
	_, release := f.createArtifactRelease(
		t,
		"printf retained-runbook-output",
	)
	body := f.api(t, "POST", f.base()+"/runbooks", map[string]any{
		"name": "obsolete", "artifact_release_id": release.ID,
		"steps": []map[string]string{{"name": "read",
			"script_body":     "printf retained-runbook-output",
			"container_image": "docker.io/library/bash:5.2"}},
	}, http.StatusCreated)
	var saved deletionRunbook
	if err := json.Unmarshal(body, &saved); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("%s/runbooks/%d", f.base(), saved.Runbook.ID)
	webPath := strings.TrimPrefix(path, "/api/v1")
	f.api(t, "PUT", path, map[string]any{
		"artifact_release_id": release.ID,
		"steps": []map[string]string{{"name": "new",
			"script_body": "printf newer", "container_image": "docker.io/library/bash:5.2"}},
	}, http.StatusCreated)
	var execution db.RunbookExecution
	if err := json.Unmarshal(f.api(t, "POST", path+"/executions",
		map[string]int64{"environment_id": f.environment.ID, "version_id": saved.Version.ID},
		http.StatusCreated), &execution); err != nil {
		t.Fatal(err)
	}
	f.completion(t, execution.DeploymentID, events.RunbookSucceeded)
	f.api(t, "POST", path+"/schedules", map[string]any{
		"environment_id": f.environment.ID, "cron": "0 3 * * *",
	}, http.StatusCreated)

	// When: a writer deletes through the API or CSRF-protected web action.
	if transport == "api" {
		if body := f.api(t, "DELETE", path, nil, http.StatusNoContent); len(
			body,
		) != 0 {
			t.Fatalf("204 has a body: %s", body)
		}
	} else {
		f.web(t, "POST", webPath+"/delete", url.Values{"name": {"forged"}}, http.StatusSeeOther)
	}

	// Then: the runbook cannot create work, but history, logs, and pins survive.
	f.api(t, "GET", path, nil, http.StatusNotFound)
	f.api(t, "DELETE", path, nil, http.StatusNotFound)
	f.api(
		t,
		"POST",
		path+"/executions",
		map[string]int64{
			"environment_id": f.environment.ID,
		},
		http.StatusNotFound,
	)
	f.api(t, "POST", path+"/schedules", map[string]any{
		"environment_id": f.environment.ID, "cron": "0 3 * * *",
	}, http.StatusNotFound)
	historyPath := fmt.Sprintf(
		"%s/runbook-executions/%d",
		f.base(),
		execution.ID,
	)
	f.api(t, "GET", historyPath, nil, http.StatusOK)
	logs := f.api(t, "GET", historyPath+"/logs", nil, http.StatusOK)
	if !strings.Contains(string(logs), "retained-runbook-output") {
		t.Fatalf("history lost original logs: %s", logs)
	}
	f.api(t, "POST", historyPath+"/retry", nil, http.StatusNotFound)
	page := f.web(
		t,
		"GET",
		fmt.Sprintf("/projects/%d/runbooks/executions/%d",
			f.project.ID, execution.ID),
		nil,
		http.StatusOK,
	)
	if strings.Contains(
		page,
		fmt.Sprintf("executions/%d/retry", execution.ID),
	) {
		t.Fatal("deleted history offers retry")
	}
	assertDeletedRunbookSnapshots(t, f, saved)
}

func assertDeletedRunbookSnapshots(
	t *testing.T, f *artifactE2E, saved deletionRunbook,
) {
	t.Helper()
	versions, err := f.h.repo.Queries.ListRunbookVersions(
		t.Context(),
		saved.Runbook.ID,
	)
	if err != nil || len(versions) != 2 {
		t.Fatalf("retained versions=%+v error=%v", versions, err)
	}
	for _, version := range versions {
		if _, err := f.h.repo.Queries.GetReleaseArtifact(t.Context(), version.ReleaseID); err != nil {
			t.Fatalf("retained pin: %v", err)
		}
		f.api(
			t,
			"GET",
			fmt.Sprintf("%s/releases/%d", f.base(), version.ReleaseID),
			nil,
			http.StatusNotFound,
		)
	}
	schedules, err := f.h.repo.Queries.ListRunbookSchedules(
		t.Context(),
		saved.Runbook.ID,
	)
	if err != nil || len(schedules) != 1 || schedules[0].Enabled != 0 {
		t.Fatalf("retained schedules=%+v error=%v", schedules, err)
	}
	var identity int64
	var details string
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT entity_id, details FROM audit_log WHERE action='delete_runbook'",
	).Scan(&identity, &details); err != nil {
		t.Fatal(err)
	}
	var audited struct {
		ProjectID int64  `json:"project_id"`
		RunbookID int64  `json:"runbook_id"`
		Name      string `json:"name"`
	}
	if err := json.Unmarshal([]byte(details), &audited); err != nil {
		t.Fatal(err)
	}
	if identity != saved.Runbook.ID ||
		audited.ProjectID != f.project.ID ||
		audited.RunbookID != saved.Runbook.ID ||
		audited.Name != saved.Runbook.Name {
		t.Fatalf("wrong audit identity: %d %+v", identity, audited)
	}
}

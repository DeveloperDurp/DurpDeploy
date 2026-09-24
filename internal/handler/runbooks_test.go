package handler_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRunbookWeb_CreateAndExecuteMultiStep(t *testing.T) {
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "development")
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(context.Background(),
		db.CreateProjectParams{Name: "runbook-web"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := h.repo.Queries.CreateEnvironment(context.Background(),
		db.CreateEnvironmentParams{Name: "runbook-env"})
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/projects/%d/runbooks", project.ID)
	form := url.Values{
		"csrf_token": {h.csrfToken()},
		"name":       {"Maintenance"},
		"step_name":  {"check", "finish"},
		"step_script": {
			"printf 'first step\\n'",
			"printf 'second step\\n'",
		},
		"step_interpreter": {"bash", "bash"},
		"step_timeout":     {"0", "0"},
		"step_retries":     {"0", "0"},
		"step_target":      {"local", "local"},
		"step_selectors":   {"", ""},
	}
	response, err := h.authedClient().
		PostForm(h.server.URL+base, form)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 303 ||
		!strings.Contains(response.Header.Get("Location"), "/runbooks/1") {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("create status=%d location=%s body=%s", response.StatusCode,
			response.Header.Get("Location"), body)
	}
	response, err = h.authedClient().PostForm(h.server.URL+base+"/1/execute",
		url.Values{
			"csrf_token":     {h.csrfToken()},
			"environment_id": {fmt.Sprint(environment.ID)},
			"version_id":     {"0"},
		})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 303 ||
		!strings.Contains(response.Header.Get("Location"), "/executions/1") {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("execute status=%d location=%s body=%s", response.StatusCode,
			response.Header.Get("Location"), body)
	}
	deadline := time.Now().Add(5 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		row, err := h.repo.Queries.GetRunbookExecution(context.Background(),
			db.GetRunbookExecutionParams{ID: 1, ProjectID: project.ID})
		if err != nil {
			t.Fatal(err)
		}
		status = row.Status
		if status == "succeeded" || status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "succeeded" {
		t.Fatalf("status=%s", status)
	}
	response, err = h.authedClient().Get(h.server.URL + base + "/executions/1")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "first step") ||
		!strings.Contains(string(body), "second step") ||
		strings.Index(string(body), "first step") >
			strings.Index(string(body), "second step") {
		t.Fatalf("execution page missing step logs: %s", body)
	}
}

func TestRunbookWeb_ViewerReadsButCannotEdit(t *testing.T) {
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(context.Background(),
		db.CreateProjectParams{Name: "viewer-runbooks"})
	if err != nil {
		t.Fatal(err)
	}
	viewer := seedSession(t, h.repo, h.server.URL, "viewer")
	if err := h.repo.Queries.AddProjectMember(context.Background(),
		db.AddProjectMemberParams{
			ProjectID: project.ID, UserID: viewer.user.ID, Role: "deployer",
		}); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/projects/%d/runbooks", project.ID)
	book, first, err := h.repo.SaveRunbook(context.Background(),
		repository.RunbookSave{
			ProjectID: project.ID,
			Name:      "Maintenance",
			StepsJSON: `[{"name":"check","script_body":"echo first_version","interpreter":"bash"}]`,
		})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.repo.SaveRunbook(context.Background(),
		repository.RunbookSave{
			ProjectID: project.ID,
			RunbookID: book.ID,
			StepsJSON: `[{"name":"check","script_body":"echo second_version","interpreter":"bash"}]`,
		}); err != nil {
		t.Fatal(err)
	}
	versionURL := fmt.Sprintf("%s/%d?version_id=%d", base, book.ID, first.ID)
	selected, err := viewer.client.Get(h.server.URL + versionURL)
	if err != nil {
		t.Fatal(err)
	}
	defer selected.Body.Close()
	selectedBody, err := io.ReadAll(selected.Body)
	if err != nil {
		t.Fatal(err)
	}
	if selected.StatusCode != http.StatusOK ||
		!strings.Contains(string(selectedBody), "echo first_version") ||
		strings.Contains(string(selectedBody), "echo second_version") {
		t.Fatalf("viewer version status=%d body=%s", selected.StatusCode,
			selectedBody)
	}
	list, err := viewer.client.Get(h.server.URL + base)
	if err != nil {
		t.Fatal(err)
	}
	defer list.Body.Close()
	if list.StatusCode != http.StatusOK {
		t.Fatalf("viewer list status=%d", list.StatusCode)
	}
	form, err := viewer.client.Get(h.server.URL + base + "/new")
	if err != nil {
		t.Fatal(err)
	}
	defer form.Body.Close()
	body, err := io.ReadAll(form.Body)
	if err != nil {
		t.Fatal(err)
	}
	if form.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), "Viewers cannot edit a runbook") ||
		strings.Contains(string(body), "Save immutable version") {
		t.Fatalf("viewer form status=%d body=%s", form.StatusCode, body)
	}
	write, err := viewer.client.PostForm(h.server.URL+base,
		url.Values{"csrf_token": {viewer.csrfToken}})
	if err != nil {
		t.Fatal(err)
	}
	defer write.Body.Close()
	if write.StatusCode != http.StatusForbidden {
		t.Fatalf("viewer write status=%d", write.StatusCode)
	}
}

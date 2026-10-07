package handler_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

// Issue #95: runbook web submissions must honor the mandatory
// server-container image and variable allowlist. Local steps run in a
// server container and require an image; agent steps stay on the remote
// host and must not carry one. Container fields must survive the
// immutable version snapshot.

func runbookContainerForm(
	csrf string,
	targets, images, names []string,
) url.Values {
	count := len(targets)
	selectors := make([]string, count)
	for i := range selectors {
		selectors[i] = ""
	}
	interpreters := make([]string, count)
	timeouts := make([]string, count)
	retries := make([]string, count)
	scripts := make([]string, count)
	stepNames := make([]string, count)
	for i := range targets {
		interpreters[i] = "bash"
		timeouts[i] = "0"
		retries[i] = "0"
		scripts[i] = fmt.Sprintf("echo step %d", i)
		stepNames[i] = fmt.Sprintf("step-%d", i)
	}
	form := url.Values{
		"csrf_token":       {csrf},
		"name":             {"Maintenance"},
		"step_name":        stepNames,
		"step_script":      scripts,
		"step_interpreter": interpreters,
		"step_timeout":     timeouts,
		"step_retries":     retries,
		"step_target":      targets,
		"step_selectors":   selectors,
	}
	if images != nil {
		form["step_image"] = images
	}
	if names != nil {
		form["step_variable_names"] = names
	}
	return form
}

func postRunbookForm(
	t *testing.T,
	h *testHarness,
	projectID int64,
	form url.Values,
) (*http.Response, string) {
	t.Helper()
	response, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/runbooks", h.server.URL, projectID),
		form,
	)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	return response, string(body)
}

func TestRunbookWeb_LocalStepRequiresContainerImage(t *testing.T) {
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(context.Background(),
		db.CreateProjectParams{Name: "runbook-no-image"})
	if err != nil {
		t.Fatal(err)
	}
	response, body := postRunbookForm(t, h, project.ID,
		runbookContainerForm(h.csrfToken(), []string{"local"}, nil, nil))
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(body, "container image is required for local steps") {
		t.Fatalf("body missing required-image message: %s", body)
	}
	books, err := h.repo.Queries.ListRunbooks(context.Background(), project.ID)
	if err != nil || len(books) != 0 {
		t.Fatalf("books=%+v error=%v", books, err)
	}
}

func TestRunbookWeb_AgentStepRejectsContainerImage(t *testing.T) {
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(context.Background(),
		db.CreateProjectParams{Name: "runbook-agent-image"})
	if err != nil {
		t.Fatal(err)
	}
	response, body := postRunbookForm(t, h, project.ID,
		runbookContainerForm(h.csrfToken(), []string{"agent"},
			[]string{"alpine:3.20"}, []string{""}))
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(
		body,
		"container image is not valid for agent-host steps",
	) {
		t.Fatalf("body missing image-rejection message: %s", body)
	}
	books, err := h.repo.Queries.ListRunbooks(context.Background(), project.ID)
	if err != nil || len(books) != 0 {
		t.Fatalf("books=%+v error=%v", books, err)
	}
}

func TestRunbookWeb_ContainerFieldsSurviveVersionSnapshot(t *testing.T) {
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(context.Background(),
		db.CreateProjectParams{Name: "runbook-snapshot"})
	if err != nil {
		t.Fatal(err)
	}
	form := runbookContainerForm(h.csrfToken(), []string{"local"},
		[]string{"alpine:3.20"}, []string{"DEPLOY_ENV, API_KEY"})
	form.Set("step_network", "bridge")
	response, body := postRunbookForm(t, h, project.ID, form)
	if response.StatusCode != http.StatusSeeOther ||
		!strings.Contains(response.Header.Get("Location"), "/runbooks/") {
		t.Fatalf("status=%d location=%s body=%s", response.StatusCode,
			response.Header.Get("Location"), body)
	}
	version, err := h.repo.Queries.GetLatestRunbookVersion(
		context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.repo.Queries.GetRelease(context.Background(),
		version.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	var steps []struct {
		ExecutionTarget string   `json:"execution_target"`
		NetworkMode     string   `json:"network_mode"`
		ContainerImage  string   `json:"container_image"`
		VariableNames   []string `json:"variable_names"`
	}
	if err := json.Unmarshal([]byte(release.StepsJson), &steps); err != nil {
		t.Fatalf("decode steps_json %q: %v", release.StepsJson, err)
	}
	if len(steps) != 1 {
		t.Fatalf("steps=%+v", steps)
	}
	if steps[0].ExecutionTarget != "local" ||
		steps[0].ContainerImage != "alpine:3.20" || steps[0].NetworkMode != "bridge" {
		t.Fatalf("step=%+v", steps[0])
	}
	if strings.Join(steps[0].VariableNames, ",") != "DEPLOY_ENV,API_KEY" {
		t.Fatalf("variable_names=%v", steps[0].VariableNames)
	}
}

func TestRunbookWeb_RejectsInvalidVariableNames(t *testing.T) {
	cases := []struct {
		name    string
		names   string
		message string
	}{
		{name: "non-identifier", names: "9BAD",
			message: "variable names must be identifiers"},
		{name: "space in name", names: "BAD NAME",
			message: "variable names must be identifiers"},
		{name: "duplicates", names: "ALPHA, ALPHA",
			message: "variable names contain duplicates"},
		{name: "reserved", names: "PODMAN_CONNECTIONS_CONF",
			message: "variable name is reserved"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			project, err := h.repo.Queries.CreateProject(
				context.Background(),
				db.CreateProjectParams{Name: "runbook-bad-vars"},
			)
			if err != nil {
				t.Fatal(err)
			}
			response, body := postRunbookForm(t, h, project.ID,
				runbookContainerForm(h.csrfToken(), []string{"local"},
					[]string{"alpine:3.20"}, []string{tc.names}))
			if response.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			if !strings.Contains(body, tc.message) {
				t.Fatalf("body missing %q: %s", tc.message, body)
			}
			books, err := h.repo.Queries.ListRunbooks(
				context.Background(), project.ID)
			if err != nil || len(books) != 0 {
				t.Fatalf("books=%+v error=%v", books, err)
			}
		})
	}
}

package handler_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"testing"
)

func TestAgentContainerWebForms(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("agent-container")
	form := url.Values{
		"name": {
			"container",
		}, "script_body": {"echo hi"}, "interpreter": {"bash"},
		"execution_target": {"agent"}, "agent_execution_mode": {"container"},
		"container_image": {"alpine:3.20"}, "variable_names": {"DEPLOY_ENV"},
		"csrf_token": {h.csrfToken()},
	}
	response, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID), form)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("step status=%d", response.StatusCode)
	}
	steps, err := h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 1 ||
		steps[0].AgentExecutionMode != "container" ||
		steps[0].ContainerImage != "alpine:3.20" ||
		steps[0].VariableNames != `["DEPLOY_ENV"]` {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
	response, err = h.authedClient().PostForm(h.server.URL+"/templates", form)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("template status=%d", response.StatusCode)
	}
	template, err := h.repo.Queries.GetStepTemplate(t.Context(), 1)
	if err != nil || template.ExecutionTarget != "agent" ||
		template.AgentExecutionMode != "container" ||
		template.ContainerImage != "alpine:3.20" {
		t.Fatalf("template=%+v error=%v", template, err)
	}
	bookForm := runbookContainerForm(
		h.csrfToken(),
		[]string{"agent"},
		[]string{"alpine:3.20"},
		[]string{"DEPLOY_ENV"},
	)
	bookForm["step_agent_mode"] = []string{"container"}
	response, err = h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/runbooks", h.server.URL, project.ID),
		bookForm)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, response)
	response.Body.Close()
	if response.StatusCode != 303 {
		t.Fatalf("runbook status=%d body=%s", response.StatusCode, body)
	}
	version, err := h.repo.Queries.GetLatestRunbookVersion(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.repo.Queries.GetRelease(t.Context(), version.ReleaseID)
	if err != nil {
		t.Fatal(err)
	}
	var snapshot []struct {
		AgentExecutionMode string `json:"agent_execution_mode"`
		ContainerImage     string `json:"container_image"`
	}
	if err := json.Unmarshal([]byte(release.StepsJson), &snapshot); err != nil {
		t.Fatal(err)
	}
	if len(snapshot) != 1 || snapshot[0].AgentExecutionMode != "container" ||
		snapshot[0].ContainerImage != "alpine:3.20" {
		t.Fatalf("snapshot=%+v", snapshot)
	}
	// Applying a template must preserve mode in both mutable and versioned records.
	history, err := h.repo.Queries.ListStepTemplateVersions(
		t.Context(),
		template.ID,
	)
	if err != nil || len(history) != 1 ||
		history[0].AgentExecutionMode != "container" {
		t.Fatalf("history=%+v error=%v", history, err)
	}
}

package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestStepWebPlacementAllowsAllEnvironmentAgents(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("all-environment-agents")
	form := url.Values{
		"name":             {"remote"},
		"script_body":      {"hostname"},
		"execution_target": {"agent"},
		"agent_label":      {""},
		"csrf_token":       {h.csrfToken()},
	}

	response, err := h.authedClient().PostForm(
		fmt.Sprintf("%s/projects/%d/steps", h.server.URL, project.ID),
		form,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	steps, err := h.repo.Queries.ListStepsByProject(t.Context(), project.ID)
	if err != nil || len(steps) != 1 || steps[0].ExecutionTarget != "agent" {
		t.Fatalf("steps=%+v error=%v", steps, err)
	}
	selectors, err := h.repo.Queries.ListStepAgentSelectors(
		t.Context(), steps[0].ID,
	)
	if err != nil || len(selectors) != 0 {
		t.Fatalf("selectors=%v error=%v", selectors, err)
	}
}

func TestStepWebPlacementPreservesAndClearsMultipleSelectors(t *testing.T) {
	h := newProjectHarness(t)
	project := h.makeProject("multiple-selectors")
	if _, err := h.repo.Queries.CreateAgent(
		t.Context(),
		db.CreateAgentParams{
			ID: "agent", Name: "agent", Endpoint: "https://agent.test",
		},
	); err != nil {
		t.Fatal(err)
	}
	for _, label := range []string{"amd64", "linux"} {
		if _, err := h.repo.Queries.AddAgentLabel(
			t.Context(),
			db.AddAgentLabelParams{AgentID: "agent", Label: label},
		); err != nil {
			t.Fatal(err)
		}
	}
	step, err := h.repo.CreateStepWithPlacement(
		t.Context(),
		db.CreateStepParams{
			ProjectID: project.ID, Name: "remote", ScriptBody: "hostname",
		},
		"agent",
		[]string{"amd64", "linux"},
	)
	if err != nil {
		t.Fatal(err)
	}

	updateStepPlacement(t, h, project.ID, step.ID, "amd64")
	selectors, err := h.repo.Queries.ListStepAgentSelectors(
		t.Context(), step.ID,
	)
	if err != nil || fmt.Sprint(selectors) != "[amd64 linux]" {
		t.Fatalf("preserved selectors=%v error=%v", selectors, err)
	}

	updateStepPlacement(t, h, project.ID, step.ID, "")
	selectors, err = h.repo.Queries.ListStepAgentSelectors(t.Context(), step.ID)
	if err != nil || len(selectors) != 0 {
		t.Fatalf("cleared selectors=%v error=%v", selectors, err)
	}
}

func updateStepPlacement(
	t *testing.T,
	h *projectHarness,
	projectID, stepID int64,
	agentLabel string,
) {
	t.Helper()
	form := url.Values{
		"name":             {"remote updated"},
		"script_body":      {"hostname"},
		"execution_target": {"agent"},
		"agent_label":      {agentLabel},
		"csrf_token":       {h.csrfToken()},
	}
	request, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPut,
		fmt.Sprintf(
			"%s/projects/%d/steps/%d", h.server.URL, projectID, stepID,
		),
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("X-CSRF-Token", h.csrfToken())
	response, err := h.authedClient().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

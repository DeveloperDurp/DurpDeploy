package handler_test

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestAdminAgentManagementFlow(t *testing.T) {
	h := newProjectHarness(t)
	environment := h.makeEnv("agent environment")
	if _, err := h.repo.Queries.CreateAgent(
		context.Background(),
		db.CreateAgentParams{
			ID: "agent-a", Name: "Original", Endpoint: "https://agent.test",
		},
	); err != nil {
		t.Fatalf("create agent: %v", err)
	}

	post := func(path string, values url.Values) *http.Response {
		t.Helper()
		values.Set("csrf_token", h.csrfToken())
		response, err := h.authedClient().PostForm(h.server.URL+path, values)
		if err != nil {
			t.Fatalf("POST %s: %v", path, err)
		}
		return response
	}

	response, err := h.authedClient().Get(h.server.URL + "/admin/agents")
	if err != nil {
		t.Fatalf("GET agents: %v", err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), "Original") {
		t.Fatalf("agent list status=%d body=%s", response.StatusCode, body)
	}

	response = post(
		"/admin/agents/agent-a/name",
		url.Values{"name": {"Renamed Agent"}},
	)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("rename status=%d", response.StatusCode)
	}

	response = post(
		"/admin/agents/agent-a/labels",
		url.Values{"label": {"linux"}},
	)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("add label status=%d", response.StatusCode)
	}

	response = post(
		"/admin/agents/agent-a/environments",
		url.Values{"environment_id": {strconv.FormatInt(environment.ID, 10)}},
	)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("add environment status=%d", response.StatusCode)
	}

	response, err = h.authedClient().Get(
		h.server.URL + "/admin/agents/agent-a",
	)
	if err != nil {
		t.Fatalf("GET agent detail: %v", err)
	}
	body, err = io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"Renamed Agent", "linux", "agent environment"} {
		if !strings.Contains(string(body), text) {
			t.Fatalf("agent detail missing %q: %s", text, body)
		}
	}

	response = post(
		"/admin/agents/agent-a/labels/delete",
		url.Values{"label": {"linux"}},
	)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete label status=%d", response.StatusCode)
	}
	response = post(
		"/admin/agents/agent-a/environments/delete",
		url.Values{"environment_id": {strconv.FormatInt(environment.ID, 10)}},
	)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("delete environment status=%d", response.StatusCode)
	}
	response = post("/admin/agents/agent-a/revoke", url.Values{})
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("revoke status=%d", response.StatusCode)
	}

	agent, err := h.repo.Queries.GetAgent(context.Background(), "agent-a")
	if err != nil {
		t.Fatalf("get agent: %v", err)
	}
	if agent.Name != "Renamed Agent" || agent.Status != "revoked" {
		t.Fatalf("agent name=%q status=%q", agent.Name, agent.Status)
	}
}

func TestViewerAgentControlsHidden(t *testing.T) {
	h := newProjectHarness(t)
	h.makeEnv("viewer environment")
	h.setRole("viewer")

	response, err := h.authedClient().Get(h.server.URL + "/environments")
	if err != nil {
		t.Fatalf("GET /environments: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(string(body), "viewer environment") ||
		strings.Contains(string(body), ">Actions</th>") ||
		strings.Contains(string(body), "name=\"agent_id\"") ||
		strings.Contains(string(body), "/admin/environments/") {
		t.Fatalf("status=%d viewer controls rendered", response.StatusCode)
	}
}

func TestViewerAgentLabelWriteReturnsToast(t *testing.T) {
	h := newProjectHarness(t)
	h.setRole("viewer")
	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodPost,
		h.server.URL+"/admin/agents/agent-a/labels",
		strings.NewReader("label=linux"),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("POST agent label: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK ||
		!strings.Contains(response.Header.Get("HX-Trigger"), "makeToast") {
		t.Fatalf(
			"status=%d trigger=%q",
			response.StatusCode,
			response.Header.Get("HX-Trigger"),
		)
	}
}

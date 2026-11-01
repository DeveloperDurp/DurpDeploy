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
	if _, err := h.repo.DB.ExecContext(
		t.Context(),
		"INSERT INTO agent_interpreters(agent_id, interpreter) "+
			"VALUES('agent-a', 'python3')",
	); err != nil {
		t.Fatalf("create agent interpreter: %v", err)
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

	checkAgentPage(t, h, "/admin/agents", "Original")

	response := post(
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
	checkAgentLabelWebPolicy(t, h, post)

	response = post(
		"/admin/agents/agent-a/environments",
		url.Values{"environment_id": {strconv.FormatInt(environment.ID, 10)}},
	)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("add environment status=%d", response.StatusCode)
	}

	checkAgentPage(t, h, "/admin/agents/agent-a",
		"Renamed Agent", "linux", "agent environment", "python3")

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

func checkAgentPage(
	t *testing.T,
	h *projectHarness,
	path string,
	texts ...string,
) {
	t.Helper()
	response, err := h.authedClient().Get(h.server.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf(
			"agent page %s status=%d body=%s",
			path,
			response.StatusCode,
			body,
		)
	}
	for _, text := range texts {
		if !strings.Contains(string(body), text) {
			t.Fatalf("agent page %s missing %q: %s", path, text, body)
		}
	}
}

func checkAgentLabelWebPolicy(
	t *testing.T,
	h *projectHarness,
	post func(string, url.Values) *http.Response,
) {
	t.Helper()
	response := post(
		"/admin/agents/agent-a/labels",
		url.Values{"label": {" \tlinux\u2003"}},
	)
	response.Body.Close()
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf("duplicate label status=%d", response.StatusCode)
	}
	var audits int
	if err := h.repo.DB.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM audit_log WHERE action='add_agent_label'",
	).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("duplicate label audits=%d err=%v", audits, err)
	}
	checkInvalidAgentLabelsWeb(t, post)
	for _, label := range []string{"x", strings.Repeat("a", 64), strings.Repeat("é", 32)} {
		for _, suffix := range []string{"", "/delete"} {
			response = post("/admin/agents/agent-a/labels"+suffix,
				url.Values{"label": {"\u2003" + label + "\t"}})
			response.Body.Close()
			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf(
					"boundary label %q %s: status=%d",
					label,
					suffix,
					response.StatusCode,
				)
			}
		}
	}
	for _, path := range []string{
		"/admin/agents/missing/labels",
		"/admin/agents/missing/labels/delete",
		"/admin/agents/agent-a/labels/delete",
	} {
		response = post(path, url.Values{"label": {"absent"}})
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf(
				"missing label/agent %s: status=%d",
				path,
				response.StatusCode,
			)
		}
	}
}

func checkInvalidAgentLabelsWeb(
	t *testing.T,
	post func(string, url.Values) *http.Response,
) {
	t.Helper()
	for _, raw := range []string{
		"", " \t\u2003", strings.Repeat("a", 65), strings.Repeat("é", 33),
	} {
		for _, suffix := range []string{"", "/delete"} {
			response := post("/admin/agents/agent-a/labels"+suffix,
				url.Values{"label": {raw}})
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil ||
				response.StatusCode != http.StatusUnprocessableEntity ||
				!strings.Contains(
					string(body),
					"Label must be between 1 and 64 characters",
				) {
				t.Fatalf(
					"invalid label %q %s: status=%d body=%s err=%v",
					raw,
					suffix,
					response.StatusCode,
					body,
					err,
				)
			}
		}
	}
}

func TestAgentLabelWebDatabaseFailures(t *testing.T) {
	h := newProjectHarness(t)
	if _, err := h.repo.Queries.CreateAgent(t.Context(), db.CreateAgentParams{
		ID: "failure-agent", Name: "Failure", Endpoint: "https://agent.test",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.DB.ExecContext(t.Context(), `
INSERT INTO agent_labels VALUES ('failure-agent','existing');
CREATE TRIGGER fail_label_add BEFORE INSERT ON agent_labels
BEGIN SELECT RAISE(FAIL, 'label insert failed'); END;
CREATE TRIGGER fail_label_delete BEFORE DELETE ON agent_labels
BEGIN SELECT RAISE(FAIL, 'label delete failed'); END`); err != nil {
		t.Fatal(err)
	}
	csrf := h.csrfToken()
	check := func(suffix, label string) {
		t.Helper()
		response, err := h.authedClient().PostForm(
			h.server.URL+"/admin/agents/failure-agent/labels"+suffix,
			url.Values{"label": {label}, "csrf_token": {csrf}},
		)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		if err != nil ||
			response.StatusCode != http.StatusInternalServerError ||
			strings.TrimSpace(string(body)) != "Internal server error" {
			t.Fatalf(
				"failure %s: status=%d body=%s err=%v",
				suffix,
				response.StatusCode,
				body,
				err,
			)
		}
	}
	check("", "new")
	check("/delete", "existing")
	if _, err := h.repo.DB.ExecContext(t.Context(),
		"ALTER TABLE agents RENAME COLUMN name TO unavailable_name",
	); err != nil {
		t.Fatal(err)
	}
	check("", "new")
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

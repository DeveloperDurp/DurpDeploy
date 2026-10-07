package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentDeleteEndToEnd(t *testing.T) {
	for _, surface := range []string{"api", "web", "htmx", "revoked"} {
		t.Run(surface, func(t *testing.T) {
			testAgentDeleteSurface(t, surface)
		})
	}
}

func agentDeleteRequest(
	t *testing.T, srv *httptest.Server, method, path string,
) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(),
		method,
		srv.URL+path,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ddp_pat_delete-admin")
	req.AddCookie(&http.Cookie{Name: "session", Value: "delete-admin"})
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { res.Body.Close() })
	return res
}

func TestAgentDeleteRejectsUnauthorizedRequests(t *testing.T) {
	for _, scenario := range []agentDeleteDeniedCase{
		{name: "anonymous api", method: "DELETE", api: true, status: 401},
		{name: "deployer api", role: "deployer", method: "DELETE", api: true, status: 403},
		{name: "viewer api", role: "viewer", method: "DELETE", api: true, status: 403},
		{name: "api session", role: "admin", method: "DELETE", status: 401},
		{name: "api body", role: "admin", method: "DELETE", api: true, body: `{"unexpected":true}`, status: 400},
		{name: "web csrf", role: "admin", method: "POST", path: "/admin/agents/agent-a/delete", status: 403},
		{name: "web deployer", role: "deployer", method: "POST", path: "/admin/agents/agent-a/delete", csrf: "csrf", status: 403},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			testAgentDeleteDeniedCase(t, scenario)
		})
	}
}

func testAgentDeleteSurface(t *testing.T, surface string) {
	t.Helper()
	// Given: a real HTTP router with an authenticated administrator.
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "delete-admin")
	seedAssignableAgent(t, h)
	if surface == "revoked" {
		if _, err := h.repo.RevokeAgent(t.Context(), "agent-a"); err != nil {
			t.Fatal(err)
		}
	}
	srv := httptest.NewServer(NewRouter(
		h.repo, h.runner, h.parser, h.authHandler,
	))
	t.Cleanup(srv.Close)
	client := srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	method, path, want := http.MethodDelete,
		"/api/v1/admin/agents/agent-a", http.StatusNoContent
	if surface == "web" || surface == "htmx" {
		method, path, want = http.MethodPost,
			"/admin/agents/agent-a/delete", http.StatusSeeOther
	}
	if surface == "htmx" {
		want = http.StatusOK
	}
	req, err := http.NewRequestWithContext(
		t.Context(),
		method,
		srv.URL+path,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ddp_pat_delete-admin")
	req.AddCookie(&http.Cookie{Name: "session", Value: "delete-admin"})
	req.Header.Set("X-CSRF-Token", "csrf")
	if surface == "htmx" {
		req.Header.Set("HX-Request", "true")
	}

	// When
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	// Then: the inventory and detail endpoints no longer expose the agent.
	if res.StatusCode != want {
		t.Fatalf("delete status=%d want=%d", res.StatusCode, want)
	}
	if surface == "htmx" &&
		res.Header.Get("HX-Redirect") != "/admin/agents" {
		t.Fatal("HTMX deletion did not navigate to inventory")
	}
	assertAgentDeleteSurface(t, h, srv)
}

func assertAgentDeleteSurface(
	t *testing.T,
	h *oidcRouterHarness,
	srv *httptest.Server,
) {
	t.Helper()
	for _, route := range []string{
		"/api/v1/admin/agents/agent-a", "/admin/agents/agent-a",
	} {
		detail := agentDeleteRequest(t, srv, http.MethodGet, route)
		if detail.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status=%d", route, detail.StatusCode)
		}
	}
	list := agentDeleteRequest(
		t,
		srv,
		http.MethodGet,
		"/api/v1/admin/agents",
	)
	var agents []agentListItem
	if err := json.NewDecoder(list.Body).Decode(&agents); err != nil {
		t.Fatal(err)
	}
	if len(agents) != 0 {
		t.Fatalf("deleted inventory=%+v", agents)
	}
	repeated := agentDeleteRequest(t, srv, http.MethodDelete,
		"/api/v1/admin/agents/agent-a")
	if repeated.StatusCode != http.StatusNotFound {
		t.Fatalf("repeat status=%d", repeated.StatusCode)
	}
	assertAuditActionCount(t, h, "delete_agent", 1)
}

type agentDeleteDeniedCase struct {
	name, role, method, path, csrf, body string
	api                                  bool
	status                               int
}

func testAgentDeleteDeniedCase(t *testing.T, scenario agentDeleteDeniedCase) {
	t.Helper()
	// Given
	h := newOIDCRouterHarness(t)
	seedAssignableAgent(t, h)
	if scenario.role != "" {
		seedAgentRouteUser(t, h, scenario.role, "delete-user")
	}
	path := scenario.path
	if path == "" {
		path = "/api/v1/admin/agents/agent-a"
	}
	req := httptest.NewRequest(
		scenario.method,
		path,
		strings.NewReader(scenario.body),
	)
	req.AddCookie(&http.Cookie{Name: "session", Value: "delete-user"})
	req.Header.Set("X-CSRF-Token", scenario.csrf)
	if scenario.api && scenario.role != "" {
		req.Header.Set("Authorization", "Bearer ddp_pat_delete-user")
	}
	res := httptest.NewRecorder()

	// When
	NewRouter(
		h.repo,
		h.runner,
		h.parser,
		h.authHandler,
	).ServeHTTP(res, req)

	// Then
	if res.Code != scenario.status {
		t.Fatalf(
			"status=%d want=%d body=%s",
			res.Code,
			scenario.status,
			res.Body,
		)
	}
	agent, err := h.repo.Queries.GetAgent(t.Context(), "agent-a")
	if err != nil || agent.Status != "active" {
		t.Fatalf("rejected deletion agent=%+v error=%v", agent, err)
	}
	assertAuditActionCount(t, h, "delete_agent", 0)
}

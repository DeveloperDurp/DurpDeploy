package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAgentRevokeEndToEnd(t *testing.T) {
	for _, surface := range []string{"api", "web", "htmx"} {
		t.Run(surface, func(t *testing.T) {
			testAgentRevokeSurface(t, surface)
		})
	}
}

func testAgentRevokeSurface(t *testing.T, surface string) {
	t.Helper()
	// Given: an active agent and an authenticated administrator.
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "delete-admin")
	seedAssignableAgent(t, h)
	srv := httptest.NewServer(NewRouter(
		h.repo, h.runner, h.parser, h.authHandler,
	))
	t.Cleanup(srv.Close)
	srv.Client().CheckRedirect = func(
		*http.Request, []*http.Request,
	) error {
		return http.ErrUseLastResponse
	}
	path, want := "/admin/agents/agent-a/revoke", http.StatusSeeOther
	switch surface {
	case "api":
		path, want = "/api/v1"+path, http.StatusNoContent
	case "htmx":
		want = http.StatusOK
	}
	req, err := http.NewRequestWithContext(t.Context(),
		http.MethodPost, srv.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ddp_pat_delete-admin")
	req.AddCookie(&http.Cookie{Name: "session", Value: "delete-admin"})
	req.Header.Set("X-CSRF-Token", "csrf")
	if surface == "htmx" {
		req.Header.Set("HX-Request", "true")
	}

	// When: revocation uses each public HTTP surface.
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()

	// Then: navigation and the public API reflect the revoked state.
	if res.StatusCode != want {
		t.Fatalf("revoke status=%d want=%d", res.StatusCode, want)
	}
	if surface == "htmx" &&
		res.Header.Get("HX-Redirect") != "/admin/agents" {
		t.Fatal("HTMX revocation did not navigate to inventory")
	}
	if surface == "web" &&
		res.Header.Get("Location") != "/admin/agents" {
		t.Fatal("form revocation did not redirect to inventory")
	}
	detail := agentDeleteRequest(t, srv, http.MethodGet,
		"/api/v1/admin/agents/agent-a")
	var result struct{ Agent struct{ Status string } }
	if err := json.NewDecoder(detail.Body).Decode(&result); err != nil {
		t.Fatal(err)
	}
	if detail.StatusCode != http.StatusOK ||
		result.Agent.Status != "revoked" {
		t.Fatalf(
			"detail status=%d result=%+v",
			detail.StatusCode,
			result,
		)
	}
	assertAuditActionCount(t, h, "revoke_agent", 1)
}

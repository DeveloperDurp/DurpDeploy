package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/agentserver"
)

type fakePairer struct {
	result agentserver.PairingResult
	calls  int
}

func (pairer *fakePairer) Pair(
	context.Context,
	agentserver.PairingInput,
) (agentserver.PairingResult, error) {
	pairer.calls++
	return pairer.result, nil
}

func TestAdminAgentPairing(t *testing.T) {
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "pair-admin")
	pairer := &fakePairer{result: agentserver.PairingResult{
		AgentID: "paired-agent", State: agentserver.PairingStatePaired,
	}}
	router := NewRouterWithAgentManagement(
		h.repo, h.runner, h.parser, h.authHandler, pairer, false,
	)
	form := url.Values{
		"address": {"https://agent.test"},
		"code": {
			base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)),
		},
		"fingerprint": {strings.Repeat("a", 64)},
		"csrf_token":  {"csrf"},
	}
	req := httptest.NewRequest(
		http.MethodPost,
		"/admin/agents/pair",
		strings.NewReader(form.Encode()),
	)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "pair-admin"})
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther || pairer.calls != 1 {
		t.Fatalf("status=%d calls=%d body=%s", res.Code, pairer.calls, res.Body)
	}
	assertAuditActionCount(t, h, "pair_agent", 1)
}

func TestAdminAssignEnvironmentAgent(t *testing.T) {
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "assign-admin")
	environmentID := seedAssignableAgent(t, h)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	form := url.Values{"agent_id": {"agent-a"}, "csrf_token": {"csrf"}}
	req := browserFormRequest(
		http.MethodPut,
		fmt.Sprintf("/admin/environments/%d/agent", environmentID),
		"assign-admin",
		form,
	)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", res.Code, res.Body)
	}
	assignment, err := h.repo.Queries.GetEnvironmentAgentAssignment(
		t.Context(), environmentID,
	)
	if err != nil || assignment.AgentID != "agent-a" {
		t.Fatalf("assignment=%+v err=%v", assignment, err)
	}
	assertAuditActionCount(t, h, "assign_environment_agent", 1)
}

func TestAdminUnassignEnvironmentAgent(t *testing.T) {
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "unassign-admin")
	environmentID := seedAssignableAgent(t, h)
	if err := h.repo.AssignEnvironmentAgent(
		t.Context(), environmentID, "agent-a",
	); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	form := url.Values{"agent_id": {"agent-a"}, "csrf_token": {"csrf"}}
	req := browserFormRequest(
		http.MethodDelete,
		fmt.Sprintf("/admin/environments/%d/agent", environmentID),
		"unassign-admin",
		form,
	)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", res.Code, res.Body)
	}
	assertAuditActionCount(t, h, "unassign_environment_agent", 1)
}

func TestAdminRevokeAgent(t *testing.T) {
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "revoke-admin")
	seedAssignableAgent(t, h)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	form := url.Values{"csrf_token": {"csrf"}}
	req := browserFormRequest(
		http.MethodPost,
		"/admin/agents/agent-a/revoke",
		"revoke-admin",
		form,
	)
	res := httptest.NewRecorder()
	router.ServeHTTP(res, req)
	if res.Code != http.StatusSeeOther {
		t.Fatalf("status=%d body=%s", res.Code, res.Body)
	}
	agent, err := h.repo.Queries.GetAgent(t.Context(), "agent-a")
	if err != nil || agent.Status != "revoked" {
		t.Fatalf("agent=%+v err=%v", agent, err)
	}
	assertAuditActionCount(t, h, "revoke_agent", 1)
}

func TestAgentAudit(t *testing.T) {
	TestAdminAssignEnvironmentAgent(t)
	TestAdminUnassignEnvironmentAgent(t)
	TestAdminRevokeAgent(t)
}

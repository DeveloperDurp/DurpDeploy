package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/agentserver"
)

type fakePairer struct {
	result          agentserver.PairingResult
	calls           int
	expectedAgentID string
}

func (pairer *fakePairer) Pair(
	_ context.Context,
	input agentserver.PairingInput,
) (agentserver.PairingResult, error) {
	pairer.calls++
	pairer.expectedAgentID = input.ExpectedAgentID()
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

func TestAdminAgentRetryBindsPairingToRouteAgent(t *testing.T) {
	// Given
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "retry-pair-admin")
	pairer := &fakePairer{result: agentserver.PairingResult{
		AgentID: "agent-a", State: agentserver.PairingStatePaired,
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
	req := browserFormRequest(
		http.MethodPost,
		"/admin/agents/agent-a/retry-pair",
		"retry-pair-admin",
		form,
	)
	res := httptest.NewRecorder()

	// When
	router.ServeHTTP(res, req)

	// Then
	if res.Code != http.StatusSeeOther ||
		pairer.expectedAgentID != "agent-a" {
		t.Fatalf(
			"status=%d expected agent ID=%q body=%s",
			res.Code,
			pairer.expectedAgentID,
			res.Body,
		)
	}
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

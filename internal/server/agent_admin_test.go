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
	challenge       agentserver.PairingChallenge
	calls           int
	beginCalls      int
	approveCalls    int
	denyCalls       int
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

func (pairer *fakePairer) Begin(
	_ context.Context,
	_ agentserver.PairingStartInput,
) (agentserver.PairingChallenge, error) {
	pairer.beginCalls++
	return pairer.challenge, nil
}

func (pairer *fakePairer) Challenge(
	id string,
) (agentserver.PairingChallenge, error) {
	if id != pairer.challenge.ID {
		return agentserver.PairingChallenge{}, agentserver.ErrPairingChallenge
	}
	return pairer.challenge, nil
}

func (pairer *fakePairer) Approve(
	_ context.Context,
	id string,
) (agentserver.PairingResult, error) {
	if id != pairer.challenge.ID {
		return agentserver.PairingResult{}, agentserver.ErrPairingChallenge
	}
	pairer.approveCalls++
	return pairer.result, nil
}

func (pairer *fakePairer) Deny(id string) error {
	if id != pairer.challenge.ID {
		return agentserver.ErrPairingChallenge
	}
	pairer.denyCalls++
	return nil
}

func TestAdminAgentPairing(t *testing.T) {
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "pair-admin")
	pairer := &fakePairer{result: agentserver.PairingResult{
		AgentID: "paired-agent", State: agentserver.PairingStatePaired,
	}, challenge: agentserver.PairingChallenge{
		ID: "confirmation", Name: "Edge runner",
		Address: "https://agent.test", Fingerprint: strings.Repeat("a", 64),
	}}
	router := NewRouterWithAgentManagement(
		h.repo, h.runner, h.parser, h.authHandler, pairer, false,
	)
	form := url.Values{
		"name":    {"Edge runner"},
		"address": {"agent.test"},
		"code": {
			base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32)),
		},
		"csrf_token": {"csrf"},
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
	if res.Code != http.StatusSeeOther || pairer.beginCalls != 1 ||
		res.Header().Get("Location") != "/admin/agents/pair/confirmation" {
		t.Fatalf(
			"status=%d begin calls=%d location=%q body=%s",
			res.Code,
			pairer.beginCalls,
			res.Header().Get("Location"),
			res.Body,
		)
	}
	assertAuditActionCount(t, h, "pair_agent", 1)

	confirmReq := httptest.NewRequest(
		http.MethodGet, "/admin/agents/pair/confirmation", nil,
	)
	confirmReq.AddCookie(&http.Cookie{Name: "session", Value: "pair-admin"})
	confirmRes := httptest.NewRecorder()
	router.ServeHTTP(confirmRes, confirmReq)
	if confirmRes.Code != http.StatusOK ||
		!strings.Contains(confirmRes.Body.String(), pairer.challenge.Fingerprint) ||
		strings.Contains(confirmRes.Body.String(), form.Get("code")) {
		t.Fatalf("confirmation status=%d body=%s", confirmRes.Code, confirmRes.Body)
	}

	approveReq := browserFormRequest(
		http.MethodPost,
		"/admin/agents/pair/confirmation/approve",
		"pair-admin",
		url.Values{"csrf_token": {"csrf"}},
	)
	approveRes := httptest.NewRecorder()
	router.ServeHTTP(approveRes, approveReq)
	if approveRes.Code != http.StatusSeeOther || pairer.approveCalls != 1 ||
		approveRes.Header().Get("Location") != "/admin/agents/paired-agent" {
		t.Fatalf(
			"approve status=%d calls=%d location=%q body=%s",
			approveRes.Code,
			pairer.approveCalls,
			approveRes.Header().Get("Location"),
			approveRes.Body,
		)
	}
	assertAuditActionCount(t, h, "approve_agent_pair", 1)
}

func TestAdminAgentPairingCanBeDenied(t *testing.T) {
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "deny-pair-admin")
	pairer := &fakePairer{challenge: agentserver.PairingChallenge{ID: "deny-me"}}
	router := NewRouterWithAgentManagement(
		h.repo, h.runner, h.parser, h.authHandler, pairer, false,
	)
	req := browserFormRequest(
		http.MethodPost,
		"/admin/agents/pair/deny-me/deny",
		"deny-pair-admin",
		url.Values{"csrf_token": {"csrf"}},
	)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	if res.Code != http.StatusSeeOther || pairer.denyCalls != 1 ||
		res.Header().Get("Location") != "/admin/agents" {
		t.Fatalf(
			"status=%d calls=%d location=%q body=%s",
			res.Code,
			pairer.denyCalls,
			res.Header().Get("Location"),
			res.Body,
		)
	}
	assertAuditActionCount(t, h, "deny_agent_pair", 1)
}

func TestAdminAgentNameCanBeChangedWithoutChangingID(t *testing.T) {
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "rename-agent-admin")
	seedAssignableAgent(t, h)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	req := browserFormRequest(
		http.MethodPost,
		"/admin/agents/agent-a/name",
		"rename-agent-admin",
		url.Values{"name": {"Chicago runner"}, "csrf_token": {"csrf"}},
	)
	res := httptest.NewRecorder()

	router.ServeHTTP(res, req)

	agent, err := h.repo.Queries.GetAgent(t.Context(), "agent-a")
	if err != nil {
		t.Fatal(err)
	}
	if res.Code != http.StatusSeeOther || agent.ID != "agent-a" ||
		agent.Name != "Chicago runner" {
		t.Fatalf("status=%d agent=%+v body=%s", res.Code, agent, res.Body)
	}
	assertAuditActionCount(t, h, "rename_agent", 1)
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

package handler_test

import (
	"context"
	"net/http"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentAudit_delete_pending_agent_uses_hx_redirect_and_audits(
	t *testing.T,
) {
	// Given
	h := newHarness(t)
	_, err := h.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{ID: "agent-delete", Name: "Agent Delete"},
	)
	if err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	before, err := h.repo.Queries.CountAuditLogs(context.Background())
	if err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodDelete,
		h.server.URL+"/admin/agents/agent-delete",
		nil,
	)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", h.csrfToken())

	// When
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	defer resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "/admin/agents" {
		t.Fatalf("HX-Redirect = %q, want /admin/agents", got)
	}
	if !hasAuditAction(t, h, "delete_agent") {
		t.Fatal("delete agent audit action is missing")
	}
	after, err := h.repo.Queries.CountAuditLogs(context.Background())
	if err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if after != before+1 {
		t.Fatalf("audit rows = %d, want %d", after, before+1)
	}
}

func TestAgentAudit_delete_pending_agent_keeps_204_without_htmx(
	t *testing.T,
) {
	// Given
	h := newHarness(t)
	_, err := h.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{ID: "agent-plain", Name: "Agent Plain"},
	)
	if err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	before, err := h.repo.Queries.CountAuditLogs(context.Background())
	if err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodDelete,
		h.server.URL+"/admin/agents/agent-plain",
		nil,
	)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	req.Header.Set("X-CSRF-Token", h.csrfToken())

	// When
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	defer resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "" {
		t.Fatalf("HX-Redirect = %q, want empty on plain delete", got)
	}
	if !hasAuditAction(t, h, "delete_agent") {
		t.Fatal("delete agent audit action is missing")
	}
	after, err := h.repo.Queries.CountAuditLogs(context.Background())
	if err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if after != before+1 {
		t.Fatalf("audit rows = %d, want %d", after, before+1)
	}
}

func TestAgentAudit_delete_pending_agent_with_history_redirects(t *testing.T) {
	// Given
	h := newHarness(t)
	created, err := h.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{
			ID:   "agent-conflict",
			Name: "Agent Conflict",
		},
	)
	if err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	_, err = h.repo.DB.ExecContext(
		context.Background(),
		"INSERT INTO agent_events (agent_id, event_type, details) VALUES (?, ?, ?)",
		created.ID,
		"heartbeat",
		"history",
	)
	if err != nil {
		t.Fatalf("create agent history: %v", err)
	}
	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodDelete,
		h.server.URL+"/admin/agents/agent-conflict",
		nil,
	)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	req.Header.Set("HX-Request", "true")
	req.Header.Set("X-CSRF-Token", h.csrfToken())

	// When
	resp, err := h.authedClient().Do(req)
	if err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	defer resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("HX-Redirect"); got != "/admin/agents" {
		t.Fatalf("HX-Redirect = %q, want /admin/agents", got)
	}
}

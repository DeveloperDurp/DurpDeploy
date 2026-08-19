package handler_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestAgentAudit_returns_redacted_history_and_preserves_historic_agent(
	t *testing.T,
) {
	// Given
	h := newHarness(t)
	created, err := h.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{ID: "agent-history", Name: "Agent History"},
	)
	if err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	now := time.Now().Unix()
	_, err = h.repo.Queries.ActivatePendingAgent(
		context.Background(),
		db.ActivatePendingAgentParams{
			ID: created.ID,
			CertificatePem: sql.NullString{
				String: "certificate",
				Valid:  true,
			},
			CertificateFingerprint: sql.NullString{
				String: strings.Repeat("a", 64),
				Valid:  true,
			},
			EnrolledAt:      sql.NullInt64{Int64: now, Valid: true},
			LastHeartbeatAt: sql.NullInt64{Int64: now, Valid: true},
			UpdatedAt:       now,
		},
	)
	if err != nil {
		t.Fatalf("activate agent: %v", err)
	}
	_, err = h.repo.DB.ExecContext(
		context.Background(),
		"INSERT INTO agent_events (agent_id, event_type, details) VALUES (?, ?, ?)",
		created.ID,
		"heartbeat",
		"secret event detail",
	)
	if err != nil {
		t.Fatalf("create agent event: %v", err)
	}

	// When
	disableResponse := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/agents/agent-history/disable",
		"",
		true,
	)
	historyResponse := adminRequest(
		t,
		h,
		h.sess,
		http.MethodGet,
		"/admin/agents/agent-history/events",
		"",
		false,
	)
	revokeResponse := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/agents/agent-history/revoke",
		"",
		true,
	)
	reenrollResponse := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/agents/agent-history/reenroll",
		"",
		true,
	)
	deleteResponse := adminRequest(
		t,
		h,
		h.sess,
		http.MethodDelete,
		"/admin/agents/agent-history",
		"",
		true,
	)

	// Then
	requireAdminStatus(t, disableResponse, http.StatusNoContent)
	requireAdminStatus(t, historyResponse, http.StatusOK)
	requireAdminStatus(t, revokeResponse, http.StatusNoContent)
	requireAdminStatus(t, reenrollResponse, http.StatusCreated)
	if reenrollResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("re-enrollment response is cacheable")
	}
	body, err := io.ReadAll(historyResponse.Body)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if strings.Contains(string(body), "secret event detail") {
		t.Fatal("agent event history contains redacted details")
	}
	requireAdminStatus(t, deleteResponse, http.StatusConflict)
	agent, err := h.repo.Queries.GetAgent(context.Background(), "agent-history")
	if err != nil || agent.Status != "pending" {
		t.Fatalf("agent after re-enrollment = %#v, %v", agent, err)
	}
	if !hasAuditAction(t, h, "disable_agent") ||
		!hasAuditAction(t, h, "revoke_agent") ||
		!hasAuditAction(t, h, "reenroll_agent") {
		t.Fatal("agent lifecycle audit actions are missing")
	}
}

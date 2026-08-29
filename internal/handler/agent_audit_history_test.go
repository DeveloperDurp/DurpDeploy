package handler_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
)

func TestAgentAudit_returns_redacted_history_and_deletes_historic_agent(
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
	codeHash := sha256.Sum256([]byte("agent-history-pairing"))
	if _, err := h.repo.Queries.CreateAgentPairing(
		context.Background(),
		db.CreateAgentPairingParams{
			AgentID:             created.ID,
			PairingCodeHash:     codeHash[:],
			AgentPublicIdentity: "certificate",
			AgentPin:            strings.Repeat("a", 64),
			ExpiresAt:           now + 60,
		},
	); err != nil {
		t.Fatalf("create pairing: %v", err)
	}
	if _, err := h.repo.Queries.BeginAgentPairing(
		context.Background(),
		db.BeginAgentPairingParams{
			AgentID: created.ID, PairingCodeHash: codeHash[:],
			AgentPin: strings.Repeat("a", 64), UpdatedAt: now, Now: now,
		},
	); err != nil {
		t.Fatalf("begin pairing: %v", err)
	}
	if _, err := h.repo.Queries.CompleteAgentPairing(
		context.Background(),
		db.CompleteAgentPairingParams{
			AgentPublicIdentity:  "certificate",
			ServerPublicIdentity: sql.NullString{String: "server", Valid: true},
			ServerPin:            sql.NullString{String: strings.Repeat("b", 64), Valid: true},
			PairedAt:             sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now,
			AgentID: created.ID, PairingCodeHash: codeHash[:],
			AgentPin: strings.Repeat("a", 64),
		},
	); err != nil {
		t.Fatalf("complete pairing: %v", err)
	}
	_, err = h.repo.Queries.ActivatePairedAgent(
		context.Background(),
		db.ActivatePairedAgentParams{
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
			PairingCodeHash: codeHash[:],
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
	body, err := io.ReadAll(historyResponse.Body)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if strings.Contains(string(body), "secret event detail") {
		t.Fatal("agent event history contains redacted details")
	}
	requireAdminStatus(t, deleteResponse, http.StatusNoContent)
	if _, err := h.repo.Queries.GetAgent(
		context.Background(),
		"agent-history",
	); err != sql.ErrNoRows {
		t.Fatalf("agent after delete = %v", err)
	}
	if !hasAuditAction(t, h, "disable_agent") ||
		!hasAuditAction(t, h, "revoke_agent") ||
		!hasAuditAction(t, h, "delete_agent") {
		t.Fatal("agent lifecycle audit actions are missing")
	}
}

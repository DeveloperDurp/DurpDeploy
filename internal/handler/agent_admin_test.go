package handler_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func adminRequest(
	t *testing.T,
	h *testHarness,
	session *authedSession,
	method, path, body string,
	withCSRF bool,
) *http.Response {
	t.Helper()
	request, err := http.NewRequest(
		method,
		h.server.URL+path,
		strings.NewReader(body),
	)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if withCSRF {
		request.Header.Set("X-CSRF-Token", session.csrfToken)
	}
	response, err := session.client.Do(request)
	if err != nil {
		t.Fatalf("send request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func requireAdminStatus(t *testing.T, response *http.Response, want int) {
	t.Helper()
	if response.StatusCode != want {
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatalf("read response: %v", err)
		}
		t.Fatalf("status = %d, want %d: %s", response.StatusCode, want, body)
	}
}

func hasAuditAction(t *testing.T, h *testHarness, want string) bool {
	t.Helper()
	entries, err := h.repo.Queries.ListAuditLogs(context.Background(), 100)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	for _, entry := range entries {
		if entry.Action == want {
			return true
		}
	}
	return false
}

func TestAgentAdmin_rejects_non_admin_and_missing_csrf_without_writes(
	t *testing.T,
) {
	// Given
	h := newHarness(t)
	deployer := seedSession(t, h.repo, h.server.URL, "deployer")
	viewer := seedSession(t, h.repo, h.server.URL, "viewer")
	before, err := h.repo.Queries.CountAuditLogs(context.Background())
	if err != nil {
		t.Fatalf("count audit logs: %v", err)
	}

	// When
	deployerResponse := adminRequest(
		t,
		h,
		deployer,
		http.MethodPost,
		"/admin/agents",
		`{"id":"not-admin","name":"Not Admin"}`,
		true,
	)
	viewerResponse := adminRequest(
		t,
		h,
		viewer,
		http.MethodPost,
		"/admin/agents",
		`{"id":"viewer","name":"Viewer"}`,
		true,
	)
	csrfResponse := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/agents",
		`{"id":"no-csrf","name":"No CSRF"}`,
		false,
	)

	// Then
	requireAdminStatus(t, deployerResponse, http.StatusForbidden)
	requireAdminStatus(t, viewerResponse, http.StatusForbidden)
	requireAdminStatus(t, csrfResponse, http.StatusForbidden)
	for _, agentID := range []string{"not-admin", "viewer", "no-csrf"} {
		if _, err := h.repo.Queries.GetAgent(
			context.Background(),
			agentID,
		); err != sql.ErrNoRows {
			t.Fatalf("agent %q was written: %v", agentID, err)
		}
	}
	after, err := h.repo.Queries.CountAuditLogs(context.Background())
	if err != nil {
		t.Fatalf("count audit logs: %v", err)
	}
	if after != before {
		t.Fatalf(
			"audit rows = %d, want %d after rejected writes",
			after,
			before,
		)
	}
}

func TestAgentAdmin_assignsAndRepairsAgents(t *testing.T) {
	// Given
	h := newHarness(t)
	_, err := h.repo.DB.ExecContext(context.Background(), `
INSERT INTO agents (id, name, status, certificate_pem, certificate_fingerprint)
VALUES ('assigned-agent', 'Assigned agent', 'active', 'certificate', ?),
       ('revoked-agent', 'Revoked agent', 'revoked', 'certificate', ?)`,
		strings.Repeat("a", 64),
		strings.Repeat("b", 64),
	)
	if err != nil {
		t.Fatalf("create agents: %v", err)
	}
	environment, err := h.repo.Queries.CreateEnvironment(
		context.Background(),
		db.CreateEnvironmentParams{Name: "assigned environment"},
	)
	if err != nil {
		t.Fatalf("create environment: %v", err)
	}
	codeHash := sha256.Sum256([]byte("assigned-agent-pairing"))
	if _, err := h.repo.Queries.CreateAgentPairing(
		context.Background(),
		db.CreateAgentPairingParams{
			AgentID:             "assigned-agent",
			PairingCodeHash:     codeHash[:],
			AgentPublicIdentity: "certificate",
			AgentPin:            strings.Repeat("a", 64),
			ExpiresAt:           2_000_000_000,
		},
	); err != nil {
		t.Fatalf("create assignment pairing: %v", err)
	}
	if _, err := h.repo.Queries.CompleteAgentPairing(
		context.Background(),
		db.CompleteAgentPairingParams{
			ServerPublicIdentity: sql.NullString{String: "server", Valid: true},
			ServerPin: sql.NullString{
				String: strings.Repeat("c", 64), Valid: true,
			},
			PairedAt:        sql.NullInt64{Int64: 1_000_000_000, Valid: true},
			UpdatedAt:       1_000_000_000,
			AgentID:         "assigned-agent",
			PairingCodeHash: codeHash[:],
			AgentPin:        strings.Repeat("a", 64),
			Now:             1,
		},
	); err != nil {
		t.Fatalf("complete assignment pairing: %v", err)
	}

	// When
	assign := h.sess.formRequest(
		t,
		h.server.URL+"/admin/agents/assigned-agent/assignments",
		url.Values{
			"environment_id": {strconv.FormatInt(environment.ID, 10)},
			"csrf_token":     {h.sess.csrfToken},
		},
	)
	assigned, err := h.sess.client.Do(assign)
	if err != nil {
		t.Fatalf("assign environment: %v", err)
	}
	defer assigned.Body.Close()
	repair := h.sess.formRequest(
		t,
		h.server.URL+"/admin/agents/revoked-agent/re-pair",
		url.Values{"csrf_token": {h.sess.csrfToken}},
	)
	repaired, err := h.sess.client.Do(repair)
	if err != nil {
		t.Fatalf("repair agent: %v", err)
	}
	defer repaired.Body.Close()

	// Then
	if assigned.StatusCode != http.StatusSeeOther ||
		repaired.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"assignment/repair status = %d/%d, want 303/303",
			assigned.StatusCode,
			repaired.StatusCode,
		)
	}
	assignment, err := h.repo.Queries.GetEnvironmentAgentAssignment(
		context.Background(), environment.ID,
	)
	if err != nil || assignment.AgentID != "assigned-agent" {
		t.Fatalf("assignment = %#v, %v", assignment, err)
	}
	repairedAgent, err := h.repo.Queries.GetAgent(
		context.Background(), "revoked-agent",
	)
	if err != nil || repairedAgent.Status != "pending" {
		t.Fatalf("repaired agent = %#v, %v", repairedAgent, err)
	}
	if !hasAuditAction(t, h, "assign_environment_agent") ||
		!hasAuditAction(t, h, "repair_agent") {
		t.Fatal("assignment or repair audit action is missing")
	}
}

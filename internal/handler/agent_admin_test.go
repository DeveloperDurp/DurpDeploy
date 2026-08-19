package handler_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
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

func TestAgentAdmin_creates_single_no_store_enrollment_token(t *testing.T) {
	// Given
	h := newHarness(t)
	response := adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/agents",
		`{"id":"agent-one","name":"Agent One"}`,
		true,
	)
	requireAdminStatus(t, response, http.StatusCreated)

	// When
	response = adminRequest(
		t,
		h,
		h.sess,
		http.MethodPost,
		"/admin/agents/agent-one/enrollment",
		"",
		true,
	)
	requireAdminStatus(t, response, http.StatusCreated)
	var enrollment struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&enrollment); err != nil {
		t.Fatalf("decode enrollment: %v", err)
	}

	// Then
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"cache-control = %q, want no-store",
			response.Header.Get("Cache-Control"),
		)
	}
	if enrollment.Token == "" {
		t.Fatal("enrollment token is empty")
	}
	if response.Request.URL.RawQuery != "" ||
		response.Header.Get("Location") != "" {
		t.Fatal("enrollment secret leaked through URL or redirect")
	}
	var stored []byte
	err := h.repo.DB.QueryRowContext(
		context.Background(),
		"SELECT token_hash FROM agent_enrollment_tokens WHERE agent_id = ?",
		"agent-one",
	).Scan(&stored)
	if err != nil {
		t.Fatalf("load enrollment hash: %v", err)
	}
	hash := sha256.Sum256([]byte(enrollment.Token))
	if !bytes.Equal(stored, hash[:]) ||
		bytes.Equal(stored, []byte(enrollment.Token)) {
		t.Fatal("database did not store only the enrollment hash")
	}
	response = adminRequest(
		t,
		h,
		h.sess,
		http.MethodGet,
		"/admin/agents/agent-one",
		"",
		false,
	)
	requireAdminStatus(t, response, http.StatusOK)
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read agent: %v", err)
	}
	if strings.Contains(string(body), enrollment.Token) {
		t.Fatal("agent response includes enrollment secret")
	}
	if !hasAuditAction(t, h, "create_agent_enrollment") {
		t.Fatal("create enrollment audit action is missing")
	}
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

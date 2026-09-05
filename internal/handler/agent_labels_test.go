package handler_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type agentLabelHarness struct {
	repo   *repository.Repository
	router http.Handler
}

func newAgentLabelHarness(t *testing.T, role string) *agentLabelHarness {
	t.Helper()
	repo := repository.New(newHandlerTestDatabase(t))
	result, err := repo.DB.ExecContext(
		context.Background(),
		"INSERT INTO users (email, password_hash, name, role) VALUES (?, 'hash', 'Label tester', ?)",
		role+"-labels@test.local",
		role,
	)
	if err != nil {
		t.Fatalf("seed label user: %v", err)
	}
	userID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("label user id: %v", err)
	}
	h := handler.NewAgentAdminHandler(repo)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, auth.SetUser(r, &db.User{ID: userID, Role: role}))
		})
	})
	router.Use(auth.RequireRole("admin"))
	router.Use(audit.Middleware(repo))
	router.Get("/admin/agent-labels", h.ListAgentLabels)
	router.Post("/admin/agent-labels", h.CreateAgentLabel)
	router.Get("/admin/agent-labels/{labelID}", h.GetAgentLabel)
	router.Put("/admin/agent-labels/{labelID}", h.UpdateAgentLabel)
	router.Delete("/admin/agent-labels/{labelID}", h.DeleteAgentLabel)
	router.Post("/admin/agent-labels/{labelID}/members", h.AddAgentLabelMember)
	router.Delete(
		"/admin/agent-labels/{labelID}/members/{agentID}",
		h.DeleteAgentLabelMember,
	)
	router.Delete("/admin/agents/{agentID}", h.DeleteAgent)
	return &agentLabelHarness{repo: repo, router: router}
}

func agentLabelRequest(
	t *testing.T,
	h *agentLabelHarness,
	method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	h.router.ServeHTTP(recorder, request)
	return recorder
}

func TestAgentLabelAdmin_CRUDMembershipAuditAndSafety(t *testing.T) {
	// Given
	h := newAgentLabelHarness(t, "admin")
	if _, err := h.repo.DB.ExecContext(context.Background(), `
INSERT INTO agents (
 id, name, status, certificate_pem, certificate_fingerprint
) VALUES
 ('paired-agent', 'Paired Agent', 'active', 'cert',
  'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa'),
 ('unpaired-agent', 'Unpaired Agent', 'active', 'cert',
  'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb');
INSERT INTO agent_pairings (
 agent_id, pairing_code_hash, agent_public_identity, agent_pin,
 server_public_identity, server_pin, state, expires_at, paired_at
) VALUES ('paired-agent',
 X'0000000000000000000000000000000000000000000000000000000000000000',
 'public', 'aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
 'server-public', 'bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
 'paired', 2000000000, 1000000000)`); err != nil {
		t.Fatalf("seed agents: %v", err)
	}

	// When
	created := agentLabelRequest(t, h, http.MethodPost,
		"/admin/agent-labels", `{"name":"  Cat Fact  "}`)

	// Then
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d: %s", created.Code, created.Body.String())
	}
	var label map[string]any
	if err := json.NewDecoder(created.Body).Decode(&label); err != nil {
		t.Fatalf("decode label: %v", err)
	}
	if label["name"] != "Cat Fact" || label["normalized_name"] != nil {
		t.Fatalf("unsafe or incorrect label = %#v", label)
	}
	duplicate := agentLabelRequest(t, h, http.MethodPost,
		"/admin/agent-labels", `{"name":"CAT FACT"}`)
	if duplicate.Code != http.StatusConflict {
		t.Fatalf("duplicate status = %d", duplicate.Code)
	}
	id := int64(label["id"].(float64))
	path := "/admin/agent-labels/" + strconv.FormatInt(id, 10)
	unpaired := agentLabelRequest(t, h, http.MethodPost, path+"/members",
		`{"agent_id":"unpaired-agent"}`)
	if unpaired.Code != http.StatusConflict {
		t.Fatalf("unpaired status = %d", unpaired.Code)
	}
	member := agentLabelRequest(t, h, http.MethodPost, path+"/members",
		`{"agent_id":"paired-agent"}`)
	if member.Code != http.StatusCreated {
		t.Fatalf("member status = %d: %s", member.Code, member.Body.String())
	}
	detail := agentLabelRequest(t, h, http.MethodGet, path, "")
	if detail.Code != http.StatusOK {
		t.Fatalf("detail status = %d", detail.Code)
	}
	renamed := agentLabelRequest(t, h, http.MethodPut, path,
		`{"name":"Cat Facts"}`)
	if renamed.Code != http.StatusOK ||
		!strings.Contains(renamed.Body.String(), `"name":"Cat Facts"`) {
		t.Fatalf("rename status = %d: %s", renamed.Code, renamed.Body.String())
	}
	if _, err := h.repo.DB.ExecContext(context.Background(),
		"UPDATE agents SET status = 'disabled' WHERE id = 'paired-agent'"); err != nil {
		t.Fatalf("disable member: %v", err)
	}
	disabledDetail := agentLabelRequest(t, h, http.MethodGet, path, "")
	if !strings.Contains(disabledDetail.Body.String(), `"eligible":false`) {
		t.Fatalf(
			"disabled membership did not become ineligible: %s",
			disabledDetail.Body.String(),
		)
	}
	if _, err := h.repo.DB.ExecContext(context.Background(), `
INSERT INTO projects (name) VALUES ('label-reference');
INSERT INTO project_execution_policies (
 project_id, target_mode, agent_label_id, agent_strategy
) VALUES (last_insert_rowid(), 'label', ?, 'all')`, id); err != nil {
		t.Fatalf("seed label reference: %v", err)
	}
	conflict := agentLabelRequest(t, h, http.MethodDelete, path, "")
	if conflict.Code != http.StatusConflict {
		t.Fatalf("referenced delete status = %d", conflict.Code)
	}
	deletedAgent := agentLabelRequest(t, h, http.MethodDelete,
		"/admin/agents/paired-agent", "")
	if deletedAgent.Code != http.StatusNoContent {
		t.Fatalf(
			"agent delete status = %d: %s",
			deletedAgent.Code,
			deletedAgent.Body.String(),
		)
	}
	var membershipCount int
	if err := h.repo.DB.QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM agent_label_memberships WHERE agent_id = 'paired-agent'",
	).Scan(&membershipCount); err != nil || membershipCount != 0 {
		t.Fatalf("agent membership count = %d, err = %v", membershipCount, err)
	}
	for _, forbidden := range []string{
		"normalized_name", "server-pin", "agent_public_identity",
	} {
		if strings.Contains(detail.Body.String(), forbidden) {
			t.Fatalf(
				"safe response leaked %q: %s",
				forbidden,
				detail.Body.String(),
			)
		}
	}
	entries, err := h.repo.Queries.ListAuditLogs(context.Background(), 100)
	if err != nil {
		t.Fatalf("list audit logs: %v", err)
	}
	actions := make(map[string]bool, len(entries))
	for _, entry := range entries {
		actions[entry.Action] = true
	}
	for _, action := range []string{
		"create_agent_label", "update_agent_label", "add_agent_label_member",
		"delete_agent",
	} {
		if !actions[action] {
			t.Errorf("missing audit action %q", action)
		}
	}
}

func TestAgentLabelAdmin_HTMLValidationAndRoleBoundary(t *testing.T) {
	// Given
	admin := newAgentLabelHarness(t, "admin")
	deployer := newAgentLabelHarness(t, "deployer")
	request := httptest.NewRequest(http.MethodPost, "/admin/agent-labels",
		strings.NewReader("name="+strings.Repeat("x", 65)))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	invalid := httptest.NewRecorder()

	// When
	admin.router.ServeHTTP(invalid, request)
	denied := agentLabelRequest(t, deployer, http.MethodPost,
		"/admin/agent-labels", `{"name":"forbidden"}`)

	// Then
	if invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid status = %d", invalid.Code)
	}
	if denied.Code != http.StatusForbidden {
		t.Fatalf("deployer status = %d", denied.Code)
	}
}

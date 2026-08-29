package handler_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentPairing_DeletePendingAgent_removesUiCreatedPairing(t *testing.T) {
	// Given
	env := newAgentPairingTestEnv(t)
	endpoint, err := url.Parse(env.bootstrapURL)
	if err != nil {
		t.Fatalf("parse bootstrap endpoint: %v", err)
	}
	create := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents",
		url.Values{
			"name":              {"Delete Me"},
			"agent_host":        {endpoint.Hostname()},
			"agent_port":        {endpoint.Port()},
			"pairing_code":      {env.code},
			"agent_fingerprint": {env.agentPin},
			"csrf_token":        {env.session.csrfToken},
		},
	)
	createResponse, err := env.session.client.Do(create)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf(
			"create status = %d, want %d",
			createResponse.StatusCode,
			http.StatusCreated,
		)
	}
	agents, err := env.repo.Queries.ListAgents(context.Background())
	if err != nil || len(agents) != 1 || agents[0].Name != "Delete Me" {
		t.Fatalf("created agents = %#v, %v", agents, err)
	}
	agentID := agents[0].ID

	// When
	deleteRequest, err := http.NewRequest(
		http.MethodDelete,
		env.server.URL+"/admin/agents/"+agentID,
		nil,
	)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	deleteRequest.Header.Set("X-CSRF-Token", env.session.csrfToken)
	deleteResponse, err := env.session.client.Do(deleteRequest)
	if err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	defer deleteResponse.Body.Close()

	// Then
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf(
			"delete status = %d, want %d",
			deleteResponse.StatusCode,
			http.StatusNoContent,
		)
	}
	if _, err := env.repo.Queries.GetAgent(
		context.Background(),
		agentID,
	); err != sql.ErrNoRows {
		t.Fatalf("agent after delete = %v", err)
	}
	if _, err := env.repo.Queries.GetAgentPairing(
		context.Background(),
		agentID,
	); err != sql.ErrNoRows {
		t.Fatalf("pairing after delete = %v", err)
	}
}

func TestAgentPairing_DeleteActiveAgent_removesPairedAgent(t *testing.T) {
	// Given
	env := newAgentPairingTestEnv(t)
	endpoint, err := url.Parse(env.bootstrapURL)
	if err != nil {
		t.Fatalf("parse bootstrap endpoint: %v", err)
	}
	create := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents",
		url.Values{
			"name":              {"Active Agent"},
			"agent_host":        {endpoint.Hostname()},
			"agent_port":        {endpoint.Port()},
			"pairing_code":      {env.code},
			"agent_fingerprint": {env.agentPin},
			"csrf_token":        {env.session.csrfToken},
		},
	)
	createResponse, err := env.session.client.Do(create)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf(
			"create status = %d, want %d",
			createResponse.StatusCode,
			http.StatusCreated,
		)
	}
	agents, err := env.repo.Queries.ListAgents(context.Background())
	if err != nil || len(agents) != 1 || agents[0].Name != "Active Agent" {
		t.Fatalf("created agents = %#v, %v", agents, err)
	}
	agentID := agents[0].ID
	confirm := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/"+agentID+"/pairings/confirm",
		url.Values{
			"agent_pin":  {env.agentPin},
			"csrf_token": {env.session.csrfToken},
		},
	)
	confirmResponse, err := env.session.client.Do(confirm)
	if err != nil {
		t.Fatalf("confirm pairing: %v", err)
	}
	defer confirmResponse.Body.Close()
	if confirmResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"confirm status = %d, want %d",
			confirmResponse.StatusCode,
			http.StatusSeeOther,
		)
	}

	// When
	deleteRequest, err := http.NewRequest(
		http.MethodDelete,
		env.server.URL+"/admin/agents/"+agentID,
		nil,
	)
	if err != nil {
		t.Fatalf("new delete request: %v", err)
	}
	deleteRequest.Header.Set("X-CSRF-Token", env.session.csrfToken)
	deleteResponse, err := env.session.client.Do(deleteRequest)
	if err != nil {
		t.Fatalf("delete agent: %v", err)
	}
	defer deleteResponse.Body.Close()

	// Then
	if deleteResponse.StatusCode != http.StatusNoContent {
		t.Fatalf(
			"delete status = %d, want %d",
			deleteResponse.StatusCode,
			http.StatusNoContent,
		)
	}
	if _, err := env.repo.Queries.GetAgent(
		context.Background(),
		agentID,
	); err != sql.ErrNoRows {
		t.Fatalf("agent after delete = %v", err)
	}
	if _, err := env.repo.Queries.GetAgentPairing(
		context.Background(),
		agentID,
	); err != sql.ErrNoRows {
		t.Fatalf("pairing after delete = %v", err)
	}
}

func TestAgentAudit_delete_pending_agent_with_history_removes_pairing(
	t *testing.T,
) {
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
	if _, err := h.repo.Queries.CreateAgentPairing(
		context.Background(),
		db.CreateAgentPairingParams{
			AgentID:             created.ID,
			PairingCodeHash:     []byte(strings.Repeat("a", 32)),
			AgentPublicIdentity: "certificate",
			AgentPin:            strings.Repeat("b", 64),
			ExpiresAt:           created.CreatedAt + 3600,
		},
	); err != nil {
		t.Fatalf("create pending agent pairing: %v", err)
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
	if _, err := h.repo.Queries.GetAgentPairing(
		context.Background(),
		created.ID,
	); err != sql.ErrNoRows {
		t.Fatalf("pairing after delete = %v", err)
	}
}

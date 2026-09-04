package handler_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAgentPairing_NewAgentForm_omitsFingerprintInput(t *testing.T) {
	// Given
	env := newAgentPairingTestEnv(t)

	// When
	response, err := env.session.client.Get(
		env.server.URL + "/admin/agents/new",
	)
	if err != nil {
		t.Fatalf("get new agent form: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read new agent form: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusOK)
	}
	if strings.Contains(string(body), `name="agent_fingerprint"`) {
		t.Fatalf("new agent form rendered a fingerprint input: %s", body)
	}
}

func TestAgentPairing_NewAgentForm_discoversFingerprintAfterCreate(
	t *testing.T,
) {
	// Given
	env := newAgentPairingTestEnv(t)
	endpoint, err := url.Parse(env.bootstrapURL)
	if err != nil {
		t.Fatalf("parse bootstrap endpoint: %v", err)
	}

	// When
	request := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents",
		url.Values{
			"name":         {"New Agent"},
			"agent_host":   {endpoint.Hostname()},
			"agent_port":   {endpoint.Port()},
			"pairing_code": {env.code},
			"csrf_token":   {env.session.csrfToken},
		},
	)
	response, err := env.session.client.Do(request)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response body: %v", err)
	}

	// Then
	if response.StatusCode != http.StatusCreated {
		t.Fatalf(
			"status = %d, want %d",
			response.StatusCode,
			http.StatusCreated,
		)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"cache-control = %q, want no-store",
			response.Header.Get("Cache-Control"),
		)
	}
	if !strings.Contains(string(body), env.agentPin) ||
		strings.Contains(string(body), env.code) ||
		strings.Contains(string(body), `name="pairing_code"`) ||
		!strings.Contains(string(body), `name="agent_pin"`) {
		t.Fatalf(
			"response body rendered a secret field instead of the confirmation page: %s",
			body,
		)
	}
	agents, err := env.repo.Queries.ListAgents(context.Background())
	if err != nil {
		t.Fatalf("list agents: %v", err)
	}
	var createdID string
	var createdStatus string
	createdCount := 0
	for _, agent := range agents {
		if agent.Name == "New Agent" {
			createdID = agent.ID
			createdStatus = agent.Status
			createdCount++
		}
	}
	if createdCount != 1 {
		t.Fatalf("created agents with name = %d, want 1", createdCount)
	}
	if _, err := uuid.Parse(createdID); err != nil {
		t.Fatalf("created agent ID = %q, want UUID: %v", createdID, err)
	}
	if createdStatus != "pending" {
		t.Fatalf("created agent status = %q, want pending", createdStatus)
	}
	if _, err := env.repo.Queries.GetAgentPairing(
		context.Background(),
		createdID,
	); err != sql.ErrNoRows {
		t.Fatalf("pairing before server-init = %v, want no rows", err)
	}
}

func TestAgentPairing_NewAgentForm_requiresTypedCode(
	t *testing.T,
) {
	// Given
	env := newAgentPairingTestEnv(t)
	endpoint, err := url.Parse(env.bootstrapURL)
	if err != nil {
		t.Fatalf("parse bootstrap endpoint: %v", err)
	}

	// When
	request := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents",
		url.Values{
			"name":       {"New Agent"},
			"agent_host": {endpoint.Hostname()},
			"agent_port": {endpoint.Port()},
			"csrf_token": {env.session.csrfToken},
		},
	)
	response, err := env.session.client.Do(request)
	if err != nil {
		t.Fatalf("create agent: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"status = %d, want %d",
			response.StatusCode,
			http.StatusUnprocessableEntity,
		)
	}
}

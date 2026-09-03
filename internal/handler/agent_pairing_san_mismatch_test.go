package handler_test

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
)

func TestAgentPairing_LocalhostBootstrapSANMismatch_promptsConfirmationThenActivates(
	t *testing.T,
) {
	// Given
	env := newAgentPairingTestEnv(t)
	endpoint, err := url.Parse(env.bootstrapURL)
	if err != nil {
		t.Fatalf("parse bootstrap endpoint: %v", err)
	}
	if _, err := env.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{ID: "paired-agent", Name: "Paired agent"},
	); err != nil {
		t.Fatalf("create pending agent: %v", err)
	}

	// When
	begin := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/paired-agent/pairings",
		url.Values{
			"agent_host":        {"localhost"},
			"agent_port":        {endpoint.Port()},
			"pairing_code":      {env.code},
			"agent_fingerprint": {env.agentPin},
			"csrf_token":        {env.session.csrfToken},
		},
	)
	beginResponse, err := env.session.client.Do(begin)
	if err != nil {
		t.Fatalf("begin pairing: %v", err)
	}
	defer beginResponse.Body.Close()
	body, err := io.ReadAll(beginResponse.Body)
	if err != nil {
		t.Fatalf("read confirmation: %v", err)
	}

	// Then
	if beginResponse.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", beginResponse.StatusCode)
	}
	if beginResponse.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf(
			"cache-control = %q, want no-store",
			beginResponse.Header.Get("Cache-Control"),
		)
	}
	if !strings.Contains(string(body), env.agentPin) {
		t.Fatalf(
			"confirmation body did not display the agent fingerprint: %s",
			body,
		)
	}
	if strings.Contains(string(body), `name="pairing_code"`) ||
		!strings.Contains(string(body), `name="agent_pin"`) {
		t.Fatalf("confirmation body rendered secret inputs: %s", body)
	}
	if _, err := env.repo.Queries.GetAgentPairing(
		context.Background(),
		"paired-agent",
	); err != sql.ErrNoRows {
		t.Fatalf("pairing before server-init = %v, want no rows", err)
	}
	created, err := env.repo.Queries.GetAgent(
		context.Background(),
		"paired-agent",
	)
	if err != nil || created.Status != "pending" {
		t.Fatalf("pending agent = %#v, %v", created, err)
	}

	confirm := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/paired-agent/pairings/confirm",
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

	if confirmResponse.StatusCode != http.StatusSeeOther ||
		confirmResponse.Header.Get("Location") != "/admin/agents/paired-agent" {
		t.Fatalf(
			"confirm response = %d %q",
			confirmResponse.StatusCode,
			confirmResponse.Header.Get("Location"),
		)
	}
	paired, err := env.repo.Queries.GetAgentPairing(
		context.Background(),
		"paired-agent",
	)
	if err != nil || paired.State != "paired" || !paired.ServerPin.Valid {
		t.Fatalf("stored pairing = %#v, %v", paired, err)
	}
	activated, err := env.repo.Queries.GetAgent(
		context.Background(),
		"paired-agent",
	)
	if err != nil || activated.Status != "active" ||
		activated.CertificateFingerprint.String != env.agentPin {
		t.Fatalf("activated paired agent = %#v, %v", activated, err)
	}
}

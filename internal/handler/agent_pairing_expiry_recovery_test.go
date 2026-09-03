package handler_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"
	"time"

	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/db"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestConfirmPairing_ReconcilesExpiredCommittedIdentityAfterOfferExpiry(
	t *testing.T,
) {
	// Given
	env := newAgentPairingTestEnvWithTTLs(
		t,
		3*time.Second,
		time.Second,
	)
	if _, err := env.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{
			ID:   "expired-commit-agent",
			Name: "Expired commit agent",
		},
	); err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	endpoint, err := url.Parse(env.bootstrapURL)
	if err != nil {
		t.Fatalf("parse bootstrap endpoint: %v", err)
	}
	begin := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/expired-commit-agent/pairings",
		url.Values{
			"agent_host":        {endpoint.Hostname()},
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
	if beginResponse.StatusCode != http.StatusCreated {
		t.Fatalf(
			"begin status = %d, want %d",
			beginResponse.StatusCode,
			http.StatusCreated,
		)
	}
	if _, err := env.repo.Queries.CreateAgentPairing(
		context.Background(),
		db.CreateAgentPairingParams{
			AgentID: "expired-commit-agent", PairingCodeHash: env.codeHash[:],
			AgentPin: env.agentPin, ExpiresAt: time.Now().Add(time.Minute).Unix(),
		},
	); err != nil {
		t.Fatalf("create durable pairing: %v", err)
	}
	if _, err := env.repo.Queries.BeginAgentPairing(
		context.Background(),
		db.BeginAgentPairingParams{
			AgentID: "expired-commit-agent", PairingCodeHash: env.codeHash[:],
			AgentPin: env.agentPin, UpdatedAt: 1, Now: 0,
		},
	); err != nil {
		t.Fatalf("begin pairing: %v", err)
	}
	serverPin, err := agentproto.ParseSHA256Pin(
		env.pairer.Identity.Fingerprint.String(),
	)
	if err != nil {
		t.Fatalf("parse server pin: %v", err)
	}
	if _, err := agentpairing.Pair(context.Background(), agentpairing.PairInput{
		Endpoint: env.bootstrapURL, AgentPin: env.bootstrap.Offer().AgentPin,
		Identity: env.pairer.Identity,
		Request: agentproto.PairRequest{
			ProtocolEnvelope: agentproto.ProtocolEnvelope{
				Protocol: agentproto.AgentV1,
			},
			PairingCode: env.bootstrap.Offer().Code,
			AgentPin:    env.bootstrap.Offer().AgentPin, ServerPin: serverPin,
			PullEndpoint: env.pairer.PullEndpoint, AgentID: "expired-commit-agent",
		},
	}); err != nil {
		t.Fatalf("persist agent pairing: %v", err)
	}
	<-time.After(3500 * time.Millisecond)

	// When
	confirm := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/expired-commit-agent/pairings/confirm",
		url.Values{
			"agent_pin":  {env.agentPin},
			"csrf_token": {env.session.csrfToken},
		},
	)
	response, err := env.session.client.Do(confirm)
	if err != nil {
		t.Fatalf("retry pairing: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"confirmation status = %d, want %d",
			response.StatusCode,
			http.StatusSeeOther,
		)
	}
	paired, err := env.repo.Queries.GetAgentPairing(
		context.Background(),
		"expired-commit-agent",
	)
	if err != nil || paired.State != "paired" {
		t.Fatalf("paired row = %#v, %v", paired, err)
	}
	select {
	case <-env.bootstrap.Paired():
	default:
		t.Fatal("listener did not receive completion acknowledgement")
	}
}

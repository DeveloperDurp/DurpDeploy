package handler_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"durpdeploy/internal/db"
	agentstate "github.com/DeveloperDurp/durpdeploy-agent/state"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestAgentPairing_ConfirmationRetriesAfterAgentCallbackFailure(
	t *testing.T,
) {
	// Given
	env := newAgentPairingTestEnv(t)
	agentIdentity, err := agenttls.LoadOrCreate(
		t.TempDir(),
		"https://127.0.0.1",
	)
	if err != nil {
		t.Fatalf("create callback agent identity: %v", err)
	}
	callbackCalls := 0
	callback := httptest.NewUnstartedServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			callbackCalls++
			if callbackCalls == 1 {
				writer.WriteHeader(http.StatusInternalServerError)
				return
			}
			writer.WriteHeader(http.StatusNoContent)
		},
	))
	callback.TLS = &tls.Config{
		Certificates: []tls.Certificate{agentIdentity.Certificate},
	}
	callback.StartTLS()
	t.Cleanup(callback.Close)
	callbackURL, err := url.Parse(callback.URL)
	if err != nil {
		t.Fatalf("parse callback endpoint: %v", err)
	}
	if _, err := env.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{ID: "retry-agent", Name: "Retry agent"},
	); err != nil {
		t.Fatalf("create pending agent: %v", err)
	}

	begin := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/retry-agent/pairings",
		url.Values{
			"agent_host":        {callbackURL.Hostname()},
			"agent_port":        {callbackURL.Port()},
			"pairing_code":      {env.code},
			"agent_fingerprint": {agentIdentity.Fingerprint.String()},
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
	confirmValues := url.Values{
		"agent_pin":  {agentIdentity.Fingerprint.String()},
		"csrf_token": {env.session.csrfToken},
	}

	// When
	for range 2 {
		confirm := env.session.formRequest(
			t,
			env.server.URL+"/admin/agents/retry-agent/pairings/confirm",
			confirmValues,
		)
		response, requestErr := env.session.client.Do(confirm)
		if requestErr != nil {
			t.Fatalf("confirm pairing: %v", requestErr)
		}
		defer response.Body.Close()
		if callbackCalls == 1 &&
			response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf(
				"failed callback status = %d, want %d",
				response.StatusCode,
				http.StatusUnprocessableEntity,
			)
		}
		if callbackCalls == 3 && response.StatusCode != http.StatusSeeOther {
			t.Fatalf(
				"retry status = %d, want %d",
				response.StatusCode,
				http.StatusSeeOther,
			)
		}
		if callbackCalls == 1 {
			committing, getErr := env.repo.Queries.GetAgentPairing(
				context.Background(),
				"retry-agent",
			)
			if getErr != nil || committing.State != "committing" {
				t.Fatalf(
					"pairing after callback failure = %#v, %v",
					committing,
					getErr,
				)
			}
		}
	}

	// Then
	paired, err := env.repo.Queries.GetAgentPairing(
		context.Background(),
		"retry-agent",
	)
	if err != nil || paired.State != "paired" || callbackCalls != 3 {
		t.Fatalf(
			"retried pairing = %#v, callbacks = %d, err = %v",
			paired,
			callbackCalls,
			err,
		)
	}
}

func TestAgentPairing_ConfirmationReconcilesAfterAgentCommitResponseLoss(
	t *testing.T,
) {
	// Given
	var dropFirstResponse sync.Once
	env := newAgentPairingTestEnvWithConfig(t, agentPairingTestConfig{
		pairingTTL: 3 * time.Second, confirmationTTL: time.Second,
		afterPairCommit: func(writer http.ResponseWriter) bool {
			dropResponse := false
			dropFirstResponse.Do(func() { dropResponse = true })
			if !dropResponse {
				return false
			}
			connection, _, err := http.NewResponseController(writer).Hijack()
			if err != nil {
				t.Errorf("hijack committed pairing response: %v", err)
				return false
			}
			if err := connection.Close(); err != nil {
				t.Errorf("close committed pairing response: %v", err)
			}
			return true
		},
	})
	if _, err := env.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{
			ID:   "response-loss-agent",
			Name: "Response loss agent",
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
		env.server.URL+"/admin/agents/response-loss-agent/pairings",
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

	// When
	firstConfirm := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/response-loss-agent/pairings/confirm",
		url.Values{
			"agent_pin":  {env.agentPin},
			"csrf_token": {env.session.csrfToken},
		},
	)
	firstResponse, err := env.session.client.Do(firstConfirm)
	if err != nil {
		t.Fatalf("first confirmation: %v", err)
	}
	defer firstResponse.Body.Close()

	// Then
	if firstResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf(
			"first confirmation status = %d, want %d",
			firstResponse.StatusCode,
			http.StatusUnprocessableEntity,
		)
	}
	committing, err := env.repo.Queries.GetAgentPairing(
		context.Background(),
		"response-loss-agent",
	)
	if err != nil || committing.State != "committing" ||
		!bytes.Equal(committing.PairingCodeHash, env.codeHash[:]) ||
		committing.AgentPin != env.agentPin {
		t.Fatalf("pairing after dropped response = %#v, %v", committing, err)
	}
	state, err := agentstate.NewStore(env.stateDir).Load()
	if err != nil || state.AgentID != "response-loss-agent" ||
		len(state.ServerPins) != 1 ||
		state.ServerPins[0].String() != env.pairer.Identity.Fingerprint.String() {
		t.Fatalf("durable agent pairing state = %#v, %v", state, err)
	}
	select {
	case <-env.bootstrap.Paired():
		t.Fatal("agent bootstrap completed before server pairing completion")
	default:
	}
	select {
	case <-env.bootstrap.Done():
		t.Fatal("agent bootstrap listener stopped after dropped response")
	default:
	}

	time.Sleep(3500 * time.Millisecond)

	// When
	confirm := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/response-loss-agent/pairings/confirm",
		url.Values{
			"agent_pin":  {env.agentPin},
			"csrf_token": {env.session.csrfToken},
		},
	)
	response, err := env.session.client.Do(confirm)
	if err != nil {
		t.Fatalf("reconcile pairing: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusSeeOther {
		t.Fatalf(
			"reconcile status = %d, want %d",
			response.StatusCode,
			http.StatusSeeOther,
		)
	}
	paired, err := env.repo.Queries.GetAgentPairing(
		context.Background(),
		"response-loss-agent",
	)
	if err != nil || paired.State != "paired" ||
		!bytes.Equal(paired.PairingCodeHash, env.codeHash[:]) ||
		paired.AgentPin != env.agentPin ||
		!paired.ServerPin.Valid ||
		paired.ServerPin.String != env.pairer.Identity.Fingerprint.String() {
		t.Fatalf("reconciled pairing = %#v, %v", paired, err)
	}
	activated, err := env.repo.Queries.GetAgent(
		context.Background(),
		"response-loss-agent",
	)
	if err != nil || activated.Status != "active" ||
		!activated.CertificateFingerprint.Valid ||
		activated.CertificateFingerprint.String != env.agentPin {
		t.Fatalf("reconciled agent = %#v, %v", activated, err)
	}
	select {
	case <-env.bootstrap.Paired():
	default:
		t.Fatal(
			"agent bootstrap did not receive server completion acknowledgement",
		)
	}
}

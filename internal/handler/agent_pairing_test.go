package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
)

func TestAgentPairing_HTMLConfirmation_persistsPairingWithoutRenderingCode(
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
	body, err := io.ReadAll(beginResponse.Body)
	if err != nil {
		t.Fatalf("read confirmation: %v", err)
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

	// Then
	if beginResponse.StatusCode != http.StatusCreated ||
		beginResponse.Header.Get("Cache-Control") != "no-store" ||
		!strings.Contains(string(body), env.agentPin) ||
		strings.Contains(string(body), env.code) ||
		strings.Contains(string(body), `name="pairing_code"`) ||
		!strings.Contains(string(body), `name="agent_pin"`) {
		t.Fatal("pairing confirmation response rendered a pairing secret")
	}
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
	if !bytes.Equal(paired.PairingCodeHash, env.codeHash[:]) {
		t.Fatal("stored pairing did not retain only the pairing-code hash")
	}
	if !hasAuditAction(
		t,
		&testHarness{repo: env.repo},
		"create_agent_pairing",
	) ||
		!hasAuditAction(
			t,
			&testHarness{repo: env.repo},
			"confirm_agent_pairing",
		) {
		t.Fatal("pairing audit actions are missing")
	}
	select {
	case <-env.bootstrap.Paired():
	case <-time.After(time.Second):
		t.Fatal(
			"agent bootstrap listener remained available after confirmation",
		)
	}
}

func TestAdminPairingFlow_RequiresOperatorTypedConfirmation(t *testing.T) {
	// Given
	env := newAgentPairingTestEnv(t)
	if _, err := env.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{ID: "typed-confirmation", Name: "Typed confirmation"},
	); err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	endpoint, err := url.Parse(env.bootstrapURL)
	if err != nil {
		t.Fatalf("parse bootstrap endpoint: %v", err)
	}
	begin := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/typed-confirmation/pairings",
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

	// When
	confirm := env.session.formRequest(
		t,
		env.server.URL+"/admin/agents/typed-confirmation/pairings/confirm",
		url.Values{
			"agent_pin":  {strings.Repeat("0", 64)},
			"csrf_token": {env.session.csrfToken},
		},
	)
	confirmResponse, err := env.session.client.Do(confirm)
	if err != nil {
		t.Fatalf("confirm pairing: %v", err)
	}
	defer confirmResponse.Body.Close()

	// Then
	if beginResponse.StatusCode != http.StatusCreated {
		t.Fatalf("begin status = %d, want %d", beginResponse.StatusCode, http.StatusCreated)
	}
	if confirmResponse.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("confirm status = %d, want %d", confirmResponse.StatusCode, http.StatusUnprocessableEntity)
	}
}

func (session *authedSession) formRequest(
	t *testing.T,
	target string,
	values url.Values,
) *http.Request {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		target,
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		t.Fatalf("create form request: %v", err)
	}
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return request
}

func pairingCodeText(t *testing.T, code agentproto.PairingCode) string {
	t.Helper()
	encoded, err := json.Marshal(code)
	if err != nil {
		t.Fatalf("encode pairing code: %v", err)
	}
	var value string
	if err := json.Unmarshal(encoded, &value); err != nil {
		t.Fatalf("decode pairing code: %v", err)
	}
	return value
}

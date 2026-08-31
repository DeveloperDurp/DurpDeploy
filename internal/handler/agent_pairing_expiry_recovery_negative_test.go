package handler_test

import (
	"context"
	"crypto/tls"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/agenttls"
	"durpdeploy/internal/db"
)

func TestConfirmPairing_RejectsExpiredNonMatchingDurablePairings(t *testing.T) {
	tests := []expiredReconciliationCase{
		{
			name: "code hash", state: "committing",
			seed: func(t *testing.T, fixture expiredReconciliationFixture) {
				t.Helper()
				codeHash := fixture.env.codeHash
				codeHash[0] ^= 0xff
				fixture.codeHash = codeHash[:]
				seedCommittingPairing(t, fixture)
			},
		},
		{
			name: "agent pin", state: "committing",
			seed: func(t *testing.T, fixture expiredReconciliationFixture) {
				t.Helper()
				fixture.agentPin = strings.Repeat("a", 64)
				seedCommittingPairing(t, fixture)
			},
		},
		{
			name: "pending state", state: "pending",
			seed: func(t *testing.T, fixture expiredReconciliationFixture) {
				t.Helper()
				seedPendingPairing(t, fixture)
			},
		},
		{
			name: "expired state", state: "expired",
			seed: func(t *testing.T, fixture expiredReconciliationFixture) {
				t.Helper()
				seedExpiredPairing(t, fixture)
			},
		},
		{
			name: "server identity", state: "paired",
			seed: func(t *testing.T, fixture expiredReconciliationFixture) {
				t.Helper()
				fixture.serverPin = strings.Repeat("a", 64)
				seedPairedPairingWithServerPin(t, fixture)
			},
		},
	}

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Given
			fixture := newExpiredReconciliationFixture(t)
			env := fixture.env
			callbackURL := fixture.callbackURL
			agentID := "expired-negative-" + string(rune('a'+index))
			if _, err := env.repo.Queries.CreatePendingAgent(
				context.Background(),
				db.CreatePendingAgentParams{ID: agentID, Name: agentID},
			); err != nil {
				t.Fatalf("create pending agent: %v", err)
			}
			begin := env.session.formRequest(
				t,
				env.server.URL+"/admin/agents/"+agentID+"/pairings",
				url.Values{
					"agent_host":        {callbackURL.Hostname()},
					"agent_port":        {callbackURL.Port()},
					"pairing_code":      {env.code},
					"agent_fingerprint": {fixture.callbackPin},
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
			fixture.agentID = agentID
			test.seed(t, fixture)
			time.Sleep(200 * time.Millisecond)

			// When
			confirm := env.session.formRequest(
				t,
				env.server.URL+"/admin/agents/"+agentID+"/pairings/confirm",
				url.Values{
					"agent_pin":  {fixture.callbackPin},
					"csrf_token": {env.session.csrfToken},
				},
			)
			response, err := env.session.client.Do(confirm)
			if err != nil {
				t.Fatalf("confirm pairing: %v", err)
			}
			defer response.Body.Close()

			// Then
			if response.StatusCode != http.StatusConflict {
				t.Fatalf(
					"confirm status = %d, want %d",
					response.StatusCode,
					http.StatusConflict,
				)
			}
			pairing, err := env.repo.Queries.GetAgentPairing(
				context.Background(),
				agentID,
			)
			if err != nil || pairing.State != test.state {
				t.Fatalf("pairing = %#v, %v", pairing, err)
			}
			agent, err := env.repo.Queries.GetAgent(
				context.Background(),
				agentID,
			)
			if err != nil || agent.Status != "pending" {
				t.Fatalf("agent = %#v, %v", agent, err)
			}
			if fixture.callbackCalls.Load() != 0 {
				t.Fatalf(
					"callback calls = %d, want 0",
					fixture.callbackCalls.Load(),
				)
			}
		})
	}
}

type expiredReconciliationCase struct {
	name  string
	state string
	seed  func(*testing.T, expiredReconciliationFixture)
}

type expiredReconciliationFixture struct {
	env           *agentPairingTestEnv
	callbackURL   *url.URL
	callbackPin   string
	callbackCalls *atomic.Int32
	agentID       string
	codeHash      []byte
	agentPin      string
	serverPin     string
}

func newExpiredReconciliationFixture(
	t *testing.T,
) expiredReconciliationFixture {
	t.Helper()
	env := newAgentPairingTestEnvWithTTLs(
		t,
		3*time.Second,
		100*time.Millisecond,
	)
	callbackIdentity, err := agenttls.LoadOrCreate(
		t.TempDir(),
		"https://127.0.0.1",
	)
	if err != nil {
		t.Fatalf("create callback identity: %v", err)
	}
	callbackCalls := &atomic.Int32{}
	callback := httptest.NewUnstartedServer(http.HandlerFunc(
		func(writer http.ResponseWriter, request *http.Request) {
			callbackCalls.Add(1)
			writer.WriteHeader(http.StatusNoContent)
		},
	))
	callback.TLS = &tls.Config{
		Certificates: []tls.Certificate{callbackIdentity.Certificate},
	}
	callback.StartTLS()
	t.Cleanup(callback.Close)
	callbackURL, err := url.Parse(callback.URL)
	if err != nil {
		t.Fatalf("parse callback URL: %v", err)
	}
	return expiredReconciliationFixture{
		env: env, callbackURL: callbackURL,
		callbackPin:   callbackIdentity.Fingerprint.String(),
		callbackCalls: callbackCalls,
		codeHash:      env.codeHash[:],
		agentPin:      callbackIdentity.Fingerprint.String(),
	}
}

func seedPendingPairing(
	t *testing.T,
	fixture expiredReconciliationFixture,
) {
	t.Helper()
	if _, err := fixture.env.repo.Queries.CreateAgentPairing(
		context.Background(),
		db.CreateAgentPairingParams{
			AgentID: fixture.agentID, PairingCodeHash: fixture.codeHash,
			AgentPin:  fixture.agentPin,
			ExpiresAt: time.Now().Add(time.Minute).Unix(),
		},
	); err != nil {
		t.Fatalf("create pairing: %v", err)
	}
}

func seedCommittingPairing(
	t *testing.T,
	fixture expiredReconciliationFixture,
) {
	t.Helper()
	seedPendingPairing(t, fixture)
	if _, err := fixture.env.repo.Queries.BeginAgentPairing(
		context.Background(),
		db.BeginAgentPairingParams{
			AgentID: fixture.agentID, PairingCodeHash: fixture.codeHash,
			AgentPin:  fixture.agentPin,
			UpdatedAt: time.Now().Unix(), Now: time.Now().Unix(),
		},
	); err != nil {
		t.Fatalf("begin pairing: %v", err)
	}
}

func seedExpiredPairing(
	t *testing.T,
	fixture expiredReconciliationFixture,
) {
	t.Helper()
	seedPendingPairing(t, fixture)
	if _, err := fixture.env.repo.Queries.ExpirePendingAgentPairings(
		context.Background(),
		db.ExpirePendingAgentPairingsParams{
			UpdatedAt: time.Now().
				Unix(),
			ExpiresBefore: time.Now().Add(time.Minute).Unix(),
		},
	); err != nil {
		t.Fatalf("expire pairing: %v", err)
	}
}

func seedPairedPairingWithServerPin(
	t *testing.T,
	fixture expiredReconciliationFixture,
) {
	t.Helper()
	seedCommittingPairing(t, fixture)
	if _, err := fixture.env.repo.Queries.CompleteAgentPairing(
		context.Background(),
		db.CompleteAgentPairingParams{
			AgentID: fixture.agentID, PairingCodeHash: fixture.codeHash,
			AgentPin:            fixture.agentPin,
			AgentPublicIdentity: "agent", ServerPublicIdentity: sql.NullString{String: "server", Valid: true},
			ServerPin: sql.NullString{String: fixture.serverPin, Valid: true},
			PairedAt:  sql.NullInt64{Int64: time.Now().Unix(), Valid: true},
			UpdatedAt: time.Now().Unix(),
		},
	); err != nil {
		t.Fatalf("complete pairing: %v", err)
	}
}

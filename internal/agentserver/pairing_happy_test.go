package agentserver

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"testing"
	"time"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"

	agentbootstrap "github.com/DeveloperDurp/durpdeploy-agent/bootstrap"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestPairingServerInitCompletes(t *testing.T) {
	fixture := newPairingFixture(t)

	result, err := fixture.pair(t)
	if err != nil {
		t.Fatal(err)
	}

	if result.State != PairingStatePaired || len(fixture.requests) != 2 ||
		fixture.requests[0].CompletionAck || !fixture.requests[1].CompletionAck {
		t.Fatalf("result=%+v requests=%+v", result, fixture.requests)
	}
	pairing, err := fixture.repo.Queries.GetAgentPairing(
		context.Background(), result.AgentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := fixture.repo.Queries.GetAgent(
		context.Background(),
		result.AgentID,
	)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := fixture.box.Decrypt(pairing.EncryptedIdentity.String)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(plain))
	if pairing.State != "paired" || agent.Status != "active" || block == nil ||
		pairing.EncryptedIdentity.String == plain ||
		pairing.ServerPullEndpoint.String != "https://server.test:10944" {
		t.Fatalf("pairing=%+v agent=%+v", pairing, agent)
	}
	assertRealPairingCompletes(t)
}

func assertRealPairingCompletes(t *testing.T) {
	t.Helper()
	listener, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: t.TempDir(), ListenAddr: "127.0.0.1:0",
		AgentVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := listener.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	}()
	connection, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	box, err := secret.NewBox(bytes.Repeat([]byte{4}, 32))
	if err != nil {
		t.Fatal(err)
	}
	identity, err := agenttls.LoadOrCreate(t.TempDir(), "https://server.test")
	if err != nil {
		t.Fatal(err)
	}
	pullEndpoint, err := agentproto.ParsePullEndpoint(
		"https://server.test:10944",
	)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewPairingService(PairingConfig{
		Repository: repository.New(connection), Identity: identity,
		PullEndpoint: pullEndpoint, Secrets: box, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	encodedCode, err := json.Marshal(listener.Offer().Code)
	if err != nil {
		t.Fatal(err)
	}
	var code string
	if err := json.Unmarshal(encodedCode, &code); err != nil {
		t.Fatal(err)
	}
	input, err := ParsePairingInput(
		listener.Endpoint(), code,
		listener.Offer().AgentPin.String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if result, err := service.Pair(context.Background(), input); err != nil ||
		result.State != PairingStatePaired {
		t.Fatalf("real pairing result=%+v err=%v", result, err)
	}
	select {
	case <-listener.Paired():
	case <-time.After(time.Second):
		t.Fatal("agent did not receive cleanup acknowledgement")
	}
}

func TestPairingPersistsTupleBeforeFirstRequest(t *testing.T) {
	fixture := newPairingFixture(t)
	requestNumber := 0
	fixture.post = func(_ agentproto.PairRequest) (int, error) {
		requestNumber++
		if requestNumber != 1 {
			return 204, nil
		}
		var state, endpoint string
		err := fixture.repo.DB.QueryRow(`SELECT state, server_pull_endpoint
			FROM agent_pairings`).Scan(&state, &endpoint)
		if err != nil || state != "committing" ||
			endpoint != "https://server.test:10944" {
			t.Fatalf("state=%q endpoint=%q err=%v", state, endpoint, err)
		}
		return 204, nil
	}

	if _, err := fixture.pair(t); err != nil {
		t.Fatal(err)
	}
}

func TestPairingCommittedTupleCompletesAfterExpiry(t *testing.T) {
	fixture := newPairingFixture(t)
	fixture.service.afterFirstPhase = func() error { return errTestInterruption }
	if _, err := fixture.pair(t); err == nil {
		t.Fatal("first attempt unexpectedly completed")
	}
	fixture.service.afterFirstPhase = nil
	fixture.now = fixture.now.Add(11 * time.Minute)
	fixture.service.now = func() time.Time { return fixture.now }

	result, err := fixture.pair(t)
	if err != nil {
		t.Fatal(err)
	}
	if result.State != PairingStatePaired {
		t.Fatalf("state=%s", result.State)
	}
	var state string
	if err := fixture.repo.DB.QueryRow(
		"SELECT state FROM agent_pairings WHERE agent_id = ?", result.AgentID,
	).Scan(&state); err != nil && err != sql.ErrNoRows {
		t.Fatal(err)
	}
	if state != "paired" {
		t.Fatalf("durable state=%q", state)
	}
}

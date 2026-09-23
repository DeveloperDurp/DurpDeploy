package agentserver_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/events"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/server"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestRemoteLogsRedactSplitSecretBeforeStorageAndBroadcast(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	box, err := secret.NewBox(bytes.Repeat([]byte{3}, 32))
	if err != nil {
		t.Fatal(err)
	}
	fixture.repo.SetSecretBox(box)
	encrypted, err := box.Encrypt("top-secret")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.DB.Exec(`INSERT INTO release_variables
		(release_id,name,value,secret) VALUES (1,'TOKEN',?,1)`, encrypted); err != nil {
		t.Fatal(err)
	}
	startLifecycle(t, fixture, http.StatusNoContent)
	stream := fixture.broker.Subscribe(1)
	t.Cleanup(func() { fixture.broker.Unsubscribe(1, stream) })
	path := strings.ReplaceAll(agentproto.LogsPath, "{id}", "1")

	response := postAgent(t, fixture, path,
		`{"protocol":"agent/1","claim_token":"claim-one",`+
			`"events":[{"sequence":1,"line":"top-"}]}`)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("first log status=%d", response.StatusCode)
	}
	var buffered string
	if err := fixture.repo.DB.QueryRow(`SELECT log_buffer_ciphertext
		FROM remote_deployment_claims WHERE deployment_id=1`).Scan(&buffered); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buffered, "top-") {
		t.Fatal("durable buffer stored plaintext")
	}
	var count int
	if err := fixture.repo.DB.QueryRow(`SELECT COUNT(*) FROM deployment_logs
		WHERE deployment_id=1`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("premature stored logs=%d error=%v", count, err)
	}
	select {
	case line := <-stream:
		t.Fatalf("premature broadcast=%q", line)
	default:
	}

	fixture = restartAgentFixture(t, fixture)
	response = postAgent(t, fixture, path,
		`{"protocol":"agent/1","claim_token":"claim-one",`+
			`"events":[{"sequence":1,"line":"top-"}]}`)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("replayed log status=%d", response.StatusCode)
	}
	response = postAgent(t, fixture, path,
		`{"protocol":"agent/1","claim_token":"claim-one",`+
			`"events":[{"sequence":2,"line":"secret appears"}]}`)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("second log status=%d", response.StatusCode)
	}

	resultPath := strings.ReplaceAll(agentproto.ResultPath, "{id}", "1")
	response = postAgent(t, fixture, resultPath,
		`{"protocol":"agent/1","claim_token":"claim-one",`+
			`"state":"succeeded","error":""}`)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("result status=%d", response.StatusCode)
	}

	logs, err := fixture.repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(), 1,
	)
	if err != nil {
		t.Fatal(err)
	}
	var stored strings.Builder
	for _, log := range logs {
		stored.WriteString(log.Line)
	}
	if got := stored.String(); got != "[REDACTED] appears" {
		t.Fatalf("stored logs=%q", got)
	}

	var streamed strings.Builder
	for range len(logs) {
		select {
		case line := <-stream:
			streamed.WriteString(line)
		default:
			t.Fatal("stored log was not broadcast")
		}
	}
	if got := streamed.String(); got != "[REDACTED] appears" {
		t.Fatalf("streamed logs=%q", got)
	}
}

func TestRemoteLogsRedactCommonPatternSplitAcrossEvents(t *testing.T) {
	fixture := newAgentFixture(t)
	seedAgentLifecycle(t, fixture.repo)
	startLifecycle(t, fixture, http.StatusNoContent)
	stream := fixture.broker.Subscribe(1)
	t.Cleanup(func() { fixture.broker.Unsubscribe(1, stream) })
	path := strings.ReplaceAll(agentproto.LogsPath, "{id}", "1")
	for _, body := range []string{
		`{"protocol":"agent/1","claim_token":"claim-one",` +
			`"events":[{"sequence":1,"line":"AKIAABCDEFGH"}]}`,
		`{"protocol":"agent/1","claim_token":"claim-one",` +
			`"events":[{"sequence":2,"line":"IJKLMNOP"}]}`,
	} {
		response := postAgent(t, fixture, path, body)
		if response.StatusCode != http.StatusNoContent {
			t.Fatalf("log status=%d", response.StatusCode)
		}
	}
	logs, err := fixture.repo.Queries.ListDeploymentLogsByDeployment(
		t.Context(), 1,
	)
	if err != nil || len(logs) != 2 ||
		logs[0].Line+logs[1].Line != "[REDACTED]" {
		t.Fatalf("stored logs=%+v error=%v", logs, err)
	}
	var streamed strings.Builder
	for range 2 {
		select {
		case line := <-stream:
			streamed.WriteString(line)
		default:
			t.Fatal("stored log was not broadcast")
		}
	}
	if got := streamed.String(); got != "[REDACTED]" {
		t.Fatalf("streamed logs=%q", got)
	}
}

func restartAgentFixture(t *testing.T, fixture agentFixture) agentFixture {
	t.Helper()
	fixture.server.Close()
	agents, err := agentserver.New(agentserver.Config{
		Repository: fixture.repo,
		Dispatcher: dispatch.New(fixture.repo),
		Identity:   fixture.serverIdentity,
		Broker:     fixture.broker,
		EventBus:   events.NewBus(fixture.repo),
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(server.NewAgentRouter(agents))
	srv.TLS = agents.TLSConfig(context.Background())
	srv.StartTLS()
	t.Cleanup(srv.Close)
	config, err := agenttls.NewClientConfig(
		srv.URL,
		[]agenttls.Fingerprint{fixture.serverIdentity.Fingerprint},
	)
	if err != nil {
		t.Fatal(err)
	}
	config.Certificates = []tls.Certificate{fixture.identity.Certificate}
	transport := &http.Transport{TLSClientConfig: config}
	t.Cleanup(transport.CloseIdleConnections)
	fixture.agents = agents
	fixture.server = srv
	fixture.client = &http.Client{Transport: transport}
	return fixture
}

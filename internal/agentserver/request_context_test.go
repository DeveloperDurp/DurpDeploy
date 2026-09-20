package agentserver_test

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentRequestContext(t *testing.T) {
	fixture := newAgentFixture(t)
	handler := fixture.agents.Authenticated(http.HandlerFunc(
		func(w http.ResponseWriter, request *http.Request) {
			agentID, ok := agentserver.AgentIDFromContext(request.Context())
			if !ok || agentID != agentproto.AgentID("test-agent") {
				t.Fatalf("agent context = %q, %v", agentID, ok)
			}
			w.WriteHeader(http.StatusNoContent)
		},
	))
	server := httptest.NewUnstartedServer(handler)
	server.TLS = fixture.agents.TLSConfig(context.Background())
	server.StartTLS()
	t.Cleanup(server.Close)

	config := fixture.client.Transport.(*http.Transport).TLSClientConfig.Clone()
	config.Certificates = []tls.Certificate{fixture.identity.Certificate}
	transport := &http.Transport{TLSClientConfig: config}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	response, err := client.Post(
		server.URL+agentproto.PollPath,
		"application/json",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", response.StatusCode)
	}
}

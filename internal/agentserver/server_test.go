package agentserver_test

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/events"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type agentFixture struct {
	repo           *repository.Repository
	agents         *agentserver.Server
	server         *httptest.Server
	client         *http.Client
	identity       agenttls.Identity
	serverIdentity agenttls.Identity
	broker         *runner.LogBroker
}

func newAgentFixture(t *testing.T) agentFixture {
	return newAgentFixtureWithDSN(
		t,
		":memory:?_pragma=foreign_keys(1)",
		nil,
	)
}

func newAgentFixtureWithDSN(
	t *testing.T,
	dsn string,
	requestStarted func(*http.Request),
) agentFixture {
	t.Helper()
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	repo := repository.New(conn)
	broker := runner.NewLogBroker()
	bus := events.NewBus(repo)
	identity, err := agenttls.LoadOrCreate(t.TempDir(), "https://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := agenttls.LoadOrCreate(t.TempDir(), "https://agent")
	if err != nil {
		t.Fatal(err)
	}
	cert := string(
		pem.EncodeToMemory(
			&pem.Block{
				Type:  "CERTIFICATE",
				Bytes: peer.Certificate.Certificate[0],
			},
		),
	)
	_, err = conn.Exec(`INSERT INTO agents
		(id,name,endpoint,status,certificate_pem,certificate_fingerprint,encrypted_identity)
		VALUES ('test-agent','test','https://agent','active',?,?,'fixture')`, cert, peer.Fingerprint.String())
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Exec(`INSERT INTO agent_pairings
		(agent_id,pairing_code_hash,agent_public_identity,agent_pin,state,expires_at,
		paired_at,server_public_identity,server_pin,encrypted_identity)
		VALUES ('test-agent',randomblob(32),?,?,'paired',1,1,'fixture',?,'fixture')`,
		cert, peer.Fingerprint.String(), identity.Fingerprint.String())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO agent_interpreters
		(agent_id,interpreter) VALUES('test-agent','bash')`); err != nil {
		t.Fatal(err)
	}
	agents, err := agentserver.New(
		agentserver.Config{
			Repository: repo,
			Dispatcher: dispatch.New(repo),
			Identity:   identity,
			Broker:     broker,
			EventBus:   bus,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	var handler http.Handler = server.NewAgentRouter(agents)
	if requestStarted != nil {
		handler = agents.Authenticated(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requestStarted(r)
				agents.Poll(w, r)
			}),
		)
	}
	srv := httptest.NewUnstartedServer(handler)
	srv.TLS = agents.TLSConfig(context.Background())
	srv.StartTLS()
	t.Cleanup(srv.Close)
	config, err := agenttls.NewClientConfig(
		srv.URL,
		[]agenttls.Fingerprint{identity.Fingerprint},
	)
	if err != nil {
		t.Fatal(err)
	}
	config.Certificates = []tls.Certificate{peer.Certificate}
	transport := &http.Transport{TLSClientConfig: config}
	t.Cleanup(transport.CloseIdleConnections)
	return agentFixture{
		repo: repo, agents: agents, server: srv,
		client:   &http.Client{Transport: transport, Timeout: 3 * time.Second},
		identity: peer, serverIdentity: identity, broker: broker,
	}
}

func TestAgentServerPinnedRoutes(t *testing.T) {
	f := newAgentFixture(t)
	seedAgentLifecycle(t, f.repo)
	for _, test := range []struct {
		path   string
		body   string
		status int
	}{
		{agentproto.StartPath, `{"protocol":"agent/1","claim_token":"claim-one"}`, http.StatusNoContent},
		{agentproto.HeartbeatPath, `{"protocol":"agent/1","claim_token":"claim-one"}`, http.StatusOK},
		{agentproto.LogsPath, `{"protocol":"agent/1","claim_token":"claim-one","events":[{"sequence":1,"line":"ok"}]}`, http.StatusNoContent},
		{agentproto.ResultPath, `{"protocol":"agent/1","claim_token":"claim-one","state":"succeeded","error":""}`, http.StatusNoContent},
		{agentproto.CancelledPath, `{"protocol":"agent/1","claim_token":"claim-two"}`, http.StatusNoContent},
		{agentproto.PollPath, `{"protocol":"agent/1","agent_version":"test"}`, http.StatusOK},
	} {
		path := strings.ReplaceAll(
			test.path,
			"{id}",
			lifecycleDeploymentID(test.path),
		)
		if test.path == agentproto.CancelledPath {
			activateCancellationFixture(t, f.repo)
		}
		response, err := f.client.Post(
			f.server.URL+path,
			"application/json",
			strings.NewReader(test.body),
		)
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != test.status ||
			response.TLS.Version != tls.VersionTLS13 ||
			response.Header.Get("Cache-Control") != "no-store" {
			t.Fatalf("route %s status=%d", path, response.StatusCode)
		}
		if test.status == http.StatusOK {
			var heartbeat agentproto.HeartbeatResponse
			if err := json.Unmarshal(body, &heartbeat); err != nil {
				t.Fatalf("route %s response: %v", path, err)
			}
		} else if len(body) != 0 {
			t.Fatalf(
				"route %s returned a body for status %d",
				path,
				test.status,
			)
		}
		t.Logf(
			"POST %s%s TLS=1.3 status=%d",
			f.server.URL,
			path,
			response.StatusCode,
		)
	}
}

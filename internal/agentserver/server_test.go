package agentserver_test

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/server"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type agentFixture struct {
	repo     *repository.Repository
	server   *httptest.Server
	client   *http.Client
	identity agenttls.Identity
}

func newAgentFixture(t *testing.T) agentFixture {
	t.Helper()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	repo := repository.New(conn)
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
	agents, err := agentserver.New(
		agentserver.Config{
			Repository: repo,
			Dispatcher: dispatch.New(repo),
			Identity:   identity,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(server.NewAgentRouter(agents))
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
		repo,
		srv,
		&http.Client{Transport: transport, Timeout: 3 * time.Second},
		peer,
	}
}

func TestAgentServerPinnedRoutes(t *testing.T) {
	f := newAgentFixture(t)
	for _, path := range []string{
		agentproto.PollPath, agentproto.StartPath,
		agentproto.HeartbeatPath, agentproto.LogsPath, agentproto.ResultPath, agentproto.CancelledPath,
	} {
		path = strings.ReplaceAll(path, "{id}", "42")
		response, err := f.client.Post(
			f.server.URL+path,
			"application/json",
			strings.NewReader("{}"),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotImplemented ||
			response.TLS.Version != tls.VersionTLS13 {
			t.Fatalf("route %s status=%d", path, response.StatusCode)
		}
		t.Logf(
			"POST %s%s TLS=1.3 status=%d",
			f.server.URL,
			path,
			response.StatusCode,
		)
	}
	if _, err := f.repo.DB.Exec("UPDATE agents SET status='disabled'"); err != nil {
		t.Fatal(err)
	}
	response, err := f.client.Post(
		f.server.URL+agentproto.PollPath,
		"application/json",
		nil,
	)
	if err != nil {
		t.Fatalf(
			"expected HTTP reauthentication on existing connection: %v",
			err,
		)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatal("disabled agent accepted")
	}
	t.Log("disabled agent rejected on existing TLS connection: status=401")
}

func TestAgentRoutesRejectBrowserSession(t *testing.T) {
	f := newAgentFixture(t)
	if _, err := f.repo.DB.Exec(`INSERT INTO users
		(email,name,password_hash,role) VALUES ('browser@test','browser','fixture','admin')`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.Exec(`INSERT INTO sessions
		(id,user_id,csrf_token,expires_at) VALUES ('browser-session',1,'browser-csrf',?)`,
		time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	config := f.client.Transport.(*http.Transport).TLSClientConfig.Clone()
	config.Certificates = nil
	transport := &http.Transport{TLSClientConfig: config}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request, err := http.NewRequest(
		http.MethodPost,
		f.server.URL+agentproto.PollPath,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(&http.Cookie{Name: "session", Value: "browser-session"})
	request.Header.Set("X-CSRF-Token", "browser-csrf")
	response, err := client.Do(request)
	if err == nil {
		response.Body.Close()
		t.Fatalf("browser-only client reached HTTP: %d", response.StatusCode)
	}
	t.Logf(
		"POST %s%s browser cookies rejected during TLS: %v",
		f.server.URL,
		agentproto.PollPath,
		err,
	)
}

func TestAgentServerRejectsUnpinnedClient(t *testing.T) {
	f := newAgentFixture(t)
	other, err := agenttls.LoadOrCreate(t.TempDir(), "https://agent")
	if err != nil {
		t.Fatal(err)
	}
	f.client.Transport.(*http.Transport).TLSClientConfig.Certificates = []tls.Certificate{
		other.Certificate,
	}
	response, err := f.client.Post(
		f.server.URL+agentproto.PollPath,
		"application/json",
		nil,
	)
	if err == nil {
		response.Body.Close()
		t.Fatal("unpinned client reached HTTP")
	}
	t.Logf("unpinned client rejected: %v", err)
}

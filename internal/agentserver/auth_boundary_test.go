package agentserver_test

import (
	"crypto/tls"
	"net/http"
	"net/http/httptrace"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestAgentServerReauthenticatesReusedConnection(t *testing.T) {
	fixture := newAgentFixture(t)
	response := postAgent(
		t,
		fixture,
		agentproto.PollPath,
		`{"protocol":"agent/1","agent_version":"v1"}`,
	)
	response.Body.Close()
	beforeHeartbeat, beforeVersion := agentState(t, fixture.repo)
	if _, err := fixture.repo.DB.Exec(
		"UPDATE agents SET status='disabled'",
	); err != nil {
		t.Fatal(err)
	}

	request, err := http.NewRequest(
		http.MethodPost,
		fixture.server.URL+agentproto.PollPath,
		strings.NewReader(`{"protocol":"agent/1","agent_version":"v2"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	var reused bool
	request = request.WithContext(httptrace.WithClientTrace(
		request.Context(),
		&httptrace.ClientTrace{GotConn: func(info httptrace.GotConnInfo) {
			reused = info.Reused
		}},
	))
	response, err = fixture.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if !reused {
		t.Fatal("second request did not reuse the authenticated connection")
	}
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"status=%d want=%d",
			response.StatusCode,
			http.StatusUnauthorized,
		)
	}
	if response.Header.Get("Cache-Control") != "no-store" {
		t.Fatal("unauthorized response is cacheable")
	}
	afterHeartbeat, afterVersion := agentState(t, fixture.repo)
	if beforeHeartbeat != afterHeartbeat || beforeVersion != afterVersion {
		t.Fatal("rejected keep-alive request changed agent state")
	}
}

func TestAgentServerRequiresClientCertificate(t *testing.T) {
	fixture := newAgentFixture(t)
	if _, err := fixture.repo.DB.Exec(`INSERT INTO users
		(email,name,password_hash,role) VALUES ('browser@test','browser','fixture','admin')`); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.repo.DB.Exec(`INSERT INTO sessions
		(id,user_id,csrf_token,expires_at) VALUES ('browser-session',1,'browser-csrf',?)`,
		time.Now().Add(time.Hour).Unix()); err != nil {
		t.Fatal(err)
	}
	config := fixture.client.Transport.(*http.Transport).TLSClientConfig.Clone()
	config.Certificates = nil
	transport := &http.Transport{TLSClientConfig: config}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.server.URL+agentproto.PollPath,
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
		fixture.server.URL,
		agentproto.PollPath,
		err,
	)
}

func TestAgentServerRejectsUnpinnedClient(t *testing.T) {
	fixture := newAgentFixture(t)
	other, err := agenttls.LoadOrCreate(t.TempDir(), "https://agent")
	if err != nil {
		t.Fatal(err)
	}
	fixture.client.Transport.(*http.Transport).TLSClientConfig.Certificates =
		[]tls.Certificate{other.Certificate}
	response, err := fixture.client.Post(
		fixture.server.URL+agentproto.PollPath,
		"application/json",
		nil,
	)
	if err == nil {
		response.Body.Close()
		t.Fatal("unpinned client reached HTTP")
	}
	t.Logf("unpinned client rejected: %v", err)
}

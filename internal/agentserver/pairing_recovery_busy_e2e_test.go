package agentserver_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/secret"
	"durpdeploy/internal/testutil"
	agentbootstrap "github.com/DeveloperDurp/durpdeploy-agent/bootstrap"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type pairingRecoveryFixture struct {
	agentFixture
	srv       *httptest.Server
	listener  *agentbootstrap.Listener
	code, dsn string
}

func newPairingRecoveryFixture(t *testing.T) pairingRecoveryFixture {
	t.Helper()
	dsn := filepath.Join(t.TempDir(), "pairing.db") +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(1)"
	f := newAgentFixtureWithDSN(t, dsn, nil)
	listener, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: t.TempDir(), ListenAddr: "127.0.0.1:0", AgentVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		if err := listener.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	box, err := secret.NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := agentproto.ParsePullEndpoint(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := agentserver.NewPairingService(
		agentserver.PairingConfig{
			Repository: f.repo, Identity: f.serverIdentity, PullEndpoint: endpoint,
			Secrets: box, Now: time.Now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	srv := fleetAdminServer(t, f, pairing)
	encoded, err := json.Marshal(listener.Offer().Code)
	if err != nil {
		t.Fatal(err)
	}
	var code string
	if err := json.Unmarshal(encoded, &code); err != nil {
		t.Fatal(err)
	}

	return pairingRecoveryFixture{
		agentFixture: f,
		srv:          srv,
		listener:     listener,
		code:         code,
		dsn:          dsn,
	}
}

func TestAgentPairingRecoveryContentionAPIWebE2E(t *testing.T) {
	for _, surface := range []string{"api", "web"} {
		t.Run(
			surface,
			func(t *testing.T) { checkPairingRecoveryContention(t, surface) },
		)
	}
}

func checkPairingRecoveryContention(t *testing.T, surface string) {
	t.Helper()
	f := newPairingRecoveryFixture(t)
	listener, code, srv := f.listener, f.code, f.srv
	body, err := json.Marshal(map[string]string{
		"address": listener.Endpoint(),
		"code": base64.RawURLEncoding.EncodeToString(
			bytes.Repeat([]byte{9}, 32),
		),
		"fingerprint": listener.Offer().AgentPin.String(),
	})
	if err != nil {
		t.Fatal(err)
	}
	fleetRequest(
		t,
		srv,
		"POST",
		"/api/v1/admin/agents/pair",
		"admin",
		string(body),
		409,
	)
	form := url.Values{"name": {"Recovered runner"},
		"address": {listener.Endpoint()}, "code": {code}}
	path := ""
	if surface == "web" {
		path = pairingRecoveryWebPost(
			t,
			srv,
			"/admin/agents/pair",
			form,
		) + "/approve"
	}
	defer holdPairingRecoveryWriter(t, f.dsn)()
	var id string
	if surface == "api" {
		body, err := json.Marshal(
			map[string]string{"address": listener.Endpoint(),
				"code": code, "fingerprint": listener.Offer().AgentPin.String()},
		)
		if err != nil {
			t.Fatal(err)
		}
		var result struct {
			AgentID string `json:"agent_id"`
		}
		if err := json.Unmarshal(fleetRequest(t, srv, "POST", "/api/v1/admin/agents/pair",
			"admin", string(body), 201), &result); err != nil {
			t.Fatal(err)
		}
		id = result.AgentID
	} else {
		id = strings.TrimPrefix(pairingRecoveryWebPost(t, srv, path, nil), "/admin/agents/")
	}
	fleetRequest(
		t,
		srv,
		"GET",
		"/api/v1/admin/agents/"+id,
		"admin",
		"",
		200,
	)
	var count int
	if err := f.agentFixture.repo.DB.QueryRowContext(t.Context(),
		"SELECT count(*) FROM agents WHERE certificate_fingerprint=? AND status='active'",
		listener.Offer().AgentPin.String()).Scan(&count); err != nil || count != 1 {
		t.Fatalf("recovered agents=%d error=%v", count, err)
	}
	select {
	case <-listener.Paired():
	default:
		t.Fatal(
			"recovery did not acknowledge the real bootstrap listener",
		)
	}
}

func holdPairingRecoveryWriter(t *testing.T, dsn string) func() {
	t.Helper()
	locker, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Close(t, locker) })
	lock, err := locker.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { testutil.Rollback(t, lock) })
	if _, err := lock.ExecContext(t.Context(),
		"UPDATE agents SET updated_at=updated_at WHERE id='test-agent'"); err != nil {
		t.Fatal(err)
	}
	released := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		released <- lock.Rollback()
	}()
	return func() {
		if err := <-released; err != nil {
			t.Error(err)
		}
	}
}

func pairingRecoveryWebPost(
	t *testing.T,
	srv *httptest.Server,
	path string,
	form url.Values,
) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), "POST", srv.URL+path,
		strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(&http.Cookie{Name: "session", Value: "fleet-admin"})
	req.Header.Set("X-CSRF-Token", "csrf")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := *srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 303 {
		t.Fatalf(
			"web pairing status=%d body=%s error=%v",
			response.StatusCode,
			body,
			err,
		)
	}
	return response.Header.Get("Location")
}

package agentserver_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/secret"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

func TestAgentPairingDeletionConflictE2E(t *testing.T) {
	for _, surface := range []string{"api", "web"} {
		t.Run(surface, func(t *testing.T) {
			testAgentPairingDeletionConflict(t, surface, false)
		})
	}
}

func TestAgentPairingDeletionAfterAcknowledgementDeadlineE2E(t *testing.T) {
	for _, surface := range []string{"api", "web"} {
		t.Run(surface, func(t *testing.T) {
			testAgentPairingDeletionConflict(t, surface, true)
		})
	}
}

func testAgentPairingDeletionConflict(
	t *testing.T,
	surface string,
	expireAck bool,
) {
	t.Helper()
	// Given: a paired identity recovered over a real TLS connection.
	f := newAgentFixture(t)
	var admin *httptest.Server
	var peerHandler http.Handler = deleteAgentOnPairingAck(t, &admin)
	if expireAck {
		completion := peerHandler
		peerHandler = http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				completion.ServeHTTP(w, r)
				// Hold the response until the pairing request's deadline disconnects.
				<-r.Context().Done()
			},
		)
	}
	peer := httptest.NewUnstartedServer(peerHandler)
	peer.TLS = &tls.Config{
		Certificates: []tls.Certificate{f.identity.Certificate},
		MinVersion:   tls.VersionTLS13,
	}
	peer.StartTLS()
	t.Cleanup(peer.Close)
	code := base64.RawURLEncoding.EncodeToString(
		bytes.Repeat([]byte{3}, 32),
	)
	parsed, err := agentproto.ParsePairingCode(code)
	if err != nil {
		t.Fatal(err)
	}
	hash := parsed.Hash()
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE",
		Bytes: f.serverIdentity.Certificate.Certificate[0]})
	if _, err := f.repo.DB.Exec(
		`UPDATE agents SET endpoint=? WHERE id='test-agent'`,
		peer.URL,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.DB.Exec(
		`UPDATE agent_pairings SET pairing_code_hash=?,
				server_public_identity=?,server_pull_endpoint=? WHERE agent_id='test-agent'`,
		hash[:],
		string(serverPEM),
		f.server.URL,
	); err != nil {
		t.Fatal(err)
	}
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
			Repository:   f.repo,
			Identity:     f.serverIdentity,
			PullEndpoint: endpoint,
			Secrets:      box,
			Now:          time.Now,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	admin = fleetAdminServer(t, f, pairing)
	if expireAck {
		handler := admin.Config.Handler
		admin.Close()
		admin = httptest.NewServer(
			http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" && strings.Contains(r.URL.Path, "pair") {
					ctx, cancel := context.WithTimeout(r.Context(), time.Second)
					defer cancel()
					r = r.WithContext(ctx)
				}
				handler.ServeHTTP(w, r)
			}),
		)
		t.Cleanup(admin.Close)
	}

	// When: deletion finishes before the agent acknowledges recovery.
	if surface == "api" {
		body := fmt.Sprintf(`{"address":%q,"code":%q,"fingerprint":%q}`,
			peer.URL, code, f.identity.Fingerprint.String())
		fleetRequest(t, admin, "POST", "/api/v1/admin/agents/pair",
			"admin", body, http.StatusConflict)
	} else {
		form := url.Values{
			"address": {peer.URL},
			"code":    {code},
			"fingerprint": {
				f.identity.Fingerprint.String(),
			},
			"csrf_token": {"csrf"},
		}
		assertWebPairingConflict(t, admin, form)
	}

	// Then: neither surface reports success for the deleted registration.
	fleetRequest(t, admin, "GET", "/api/v1/admin/agents/test-agent",
		"admin", "", http.StatusNotFound)
}

func deleteAgentOnPairingAck(
	t *testing.T,
	admin **httptest.Server,
) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var payload agentproto.PairRequest
		if err := json.NewDecoder(r.Body).
			Decode(&payload); err != nil ||
			!payload.CompletionAck {
			t.Errorf("completion request=%+v error=%v", payload, err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), "DELETE",
			(*admin).URL+"/api/v1/admin/agents/test-agent", nil)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		request.Header.Set("Authorization", "Bearer ddp_pat_fleet_admin")
		response, err := (*admin).Client().Do(request)
		if err != nil {
			t.Error(err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNoContent {
			t.Errorf("delete status=%d", response.StatusCode)
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func assertWebPairingConflict(
	t *testing.T,
	admin *httptest.Server,
	form url.Values,
) {
	t.Helper()
	request, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		admin.URL+"/admin/agents/test-agent/retry-pair",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.AddCookie(&http.Cookie{Name: "session", Value: "fleet-admin"})
	client := *admin.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("web pairing status=%d", response.StatusCode)
	}
}

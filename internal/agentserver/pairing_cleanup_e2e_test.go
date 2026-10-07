package agentserver_test

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/secret"
	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestAgentPairingCleanupConflictAPIE2E(t *testing.T) {
	f := newAgentFixture(t)
	deploymentID := seedPollPayload(t, f, "pending", "test-agent")
	if _, err := f.repo.DB.Exec(`UPDATE remote_deployment_claims
SET state='cleanup_unconfirmed',claim_token_hash=zeroblob(32),
ciphertext='ciphertext',claim_expires_at=200,last_heartbeat_at=100,
started_at=100,finished_at=101 WHERE deployment_id=?`, deploymentID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.RevokeAgent(t.Context(), "test-agent"); err != nil {
		t.Fatal(err)
	}
	replacement, err := agenttls.LoadOrCreate(t.TempDir(), "https://127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int64
	peer := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			requests.Add(1)
			w.WriteHeader(http.StatusNoContent)
		}),
	)
	peer.TLS = &tls.Config{
		Certificates: []tls.Certificate{replacement.Certificate},
		MinVersion:   tls.VersionTLS13,
	}
	peer.StartTLS()
	t.Cleanup(peer.Close)
	box, err := secret.NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	endpoint, err := agentproto.ParsePullEndpoint(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := agentserver.NewPairingService(agentserver.PairingConfig{
		Repository: f.repo, Identity: f.serverIdentity, PullEndpoint: endpoint,
		Secrets: box, Now: time.Now,
	})
	if err != nil {
		t.Fatal(err)
	}
	srv := fleetAdminServer(t, f, pairing)
	body := fmt.Sprintf(
		`{"address":%q,"code":%q,"fingerprint":%q}`,
		peer.URL,
		base64.RawURLEncoding.EncodeToString(
			bytes.Repeat([]byte{9}, 32),
		),
		replacement.Fingerprint.String(),
	)
	path := "/api/v1/admin/agents/test-agent/retry-pair"
	fleetRequest(t, srv, "POST", path, "admin", body, http.StatusConflict)
	form := url.Values{
		"address": {peer.URL},
		"code": {
			base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32)),
		},
		"fingerprint": {
			replacement.Fingerprint.String(),
		},
		"csrf_token": {"csrf"},
	}
	req, err := http.NewRequestWithContext(
		t.Context(),
		"POST",
		srv.URL+"/admin/agents/test-agent/retry-pair",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(&http.Cookie{Name: "session", Value: "fleet-admin"})
	response, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("web cleanup conflict status=%d", response.StatusCode)
	}
	// Revocation can leave lost work while an authenticated result is in flight.
	for _, state := range []string{"lost", "cancel_unconfirmed"} {
		var cancelRequestedAt any
		if state == "cancel_unconfirmed" {
			cancelRequestedAt = 100
		}
		if _, err := f.repo.DB.Exec(`UPDATE remote_deployment_claims
SET state=?,cancel_requested_at=? WHERE deployment_id=?`,
			state, cancelRequestedAt, deploymentID); err != nil {
			t.Fatal(err)
		}
		fleetRequest(t, srv, "POST", path, "admin", body, http.StatusConflict)
	}
	if requests.Load() != 0 {
		t.Fatal(
			"replacement received pairing while old cleanup was unconfirmed",
		)
	}
	if _, err := f.repo.DB.Exec(`UPDATE remote_deployment_claims
SET state='cleanup_unconfirmed',cleanup_confirmed_at=200 WHERE deployment_id=?`, deploymentID); err != nil {
		t.Fatal(err)
	}
	fleetRequest(t, srv, "POST", path, "admin", body, http.StatusCreated)
	if requests.Load() != 2 {
		t.Fatalf("replacement pairing requests=%d", requests.Load())
	}
}

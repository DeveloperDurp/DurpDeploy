package agentclient

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"durpdeploy/internal/agentpayload"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/agenttls"
)

func TestClient_polls_and_acknowledges_cancellation(t *testing.T) {
	// Given
	serverIdentity := testIdentity(t)
	requests := make(chan string, 2)
	server := newTLSServer(
		t,
		serverIdentity,
		func(w http.ResponseWriter, r *http.Request) {
			requests <- r.URL.Path
			switch r.URL.Path {
			case agentproto.PollPath:
				_ = json.NewEncoder(w).Encode(agentproto.PollResponse{
					DeploymentID: 42, Payload: "ciphertext", ClaimToken: "claim-secret",
				})
			case "/agent/v1/deployments/42/heartbeat":
				_ = json.NewEncoder(w).Encode(agentproto.HeartbeatResponse{
					CancelRequested: true,
					ServerPins: []agentproto.CertificateFingerprint{
						agentproto.CertificateFingerprint(
							serverIdentity.Fingerprint.String(),
						),
					},
				})
			default:
				w.WriteHeader(http.StatusNotFound)
			}
		},
	)
	client := testClient(t, server.URL, serverIdentity.Fingerprint.String())

	// When
	poll, err := client.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	heartbeat, err := client.Heartbeat(
		context.Background(),
		poll.DeploymentID,
		poll.ClaimToken,
	)
	if err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	// Then
	if poll == nil || poll.ClaimToken != "claim-secret" ||
		!heartbeat.CancelRequested {
		t.Fatalf("unexpected poll or heartbeat response")
	}
	for _, want := range []string{agentproto.PollPath, "/agent/v1/deployments/42/heartbeat"} {
		if got := <-requests; got != want {
			t.Fatalf("request path = %q, want %q", got, want)
		}
	}
}

func TestClient_retries_retryable_statuses_with_retry_after(t *testing.T) {
	// Given
	serverIdentity := testIdentity(t)
	calls := 0
	server := newTLSServer(
		t,
		serverIdentity,
		func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			if calls == 2 {
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		},
	)
	client := testClient(t, server.URL, serverIdentity.Fingerprint.String())
	var delays []time.Duration
	client.sleep = func(_ context.Context, delay time.Duration) error {
		delays = append(delays, delay)
		return nil
	}
	client.jitter = func(limit int64) (int64, error) { return limit - 1, nil }

	// When
	_, err := client.Poll(context.Background())

	// Then
	if err != nil || calls != 3 || len(delays) != 2 ||
		delays[0] != 2*time.Second {
		t.Fatalf("err=%v calls=%d delays=%v", err, calls, delays)
	}
}

func TestClient_rejects_unsupported_protocol_and_oversized_body(t *testing.T) {
	// Given
	serverIdentity := testIdentity(t)
	server := newTLSServer(
		t,
		serverIdentity,
		func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == agentproto.PollPath {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(make([]byte, agentproto.MaxRequestBytes+1))
			}
		},
	)
	client := testClient(t, server.URL, serverIdentity.Fingerprint.String())
	client.protocol = "agent/99"

	// When
	_, err := client.Poll(context.Background())

	// Then
	if err == nil {
		t.Fatal("poll accepted an unsupported protocol or oversized response")
	}
}

func TestClient_persists_staged_server_pin_from_heartbeat(t *testing.T) {
	// Given
	serverIdentity := testIdentity(t)
	pendingIdentity := testIdentity(t)
	server := newTLSServer(
		t,
		serverIdentity,
		func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewEncoder(w).
				Encode(agentproto.HeartbeatResponse{ServerPins: []agentproto.CertificateFingerprint{
					agentproto.CertificateFingerprint(serverIdentity.Fingerprint.String()),
					agentproto.CertificateFingerprint(pendingIdentity.Fingerprint.String()),
				}})
		},
	)
	client := testClient(t, server.URL, serverIdentity.Fingerprint.String())

	// When
	_, err := client.Heartbeat(context.Background(), 42, "claim-secret")

	// Then
	if err != nil || len(client.pins) != 2 ||
		client.pins[1] != pendingIdentity.Fingerprint {
		t.Fatalf("heartbeat error=%v pins=%v", err, client.pins)
	}
	state, err := agentstate.NewStore(client.stateDir).Load()
	if err != nil || len(state.ServerPins) != 2 ||
		state.ServerPins[1] != pendingIdentity.Fingerprint {
		t.Fatalf("persisted pins error=%v pins=%v", err, state.ServerPins)
	}
}

func TestClient_sends_every_lifecycle_mutation(t *testing.T) {
	// Given
	serverIdentity := testIdentity(t)
	paths := make(chan string, 4)
	server := newTLSServer(
		t,
		serverIdentity,
		func(w http.ResponseWriter, r *http.Request) {
			paths <- r.URL.Path
			w.WriteHeader(http.StatusNoContent)
		},
	)
	client := testClient(t, server.URL, serverIdentity.Fingerprint.String())

	// When
	err := client.Start(context.Background(), 42, "claim-secret")
	if err == nil {
		err = client.Logs(
			context.Background(),
			42,
			agentproto.LogBatchRequest{ClaimToken: "claim-secret"},
		)
	}
	if err == nil {
		err = client.Result(
			context.Background(),
			42,
			agentproto.ResultRequest{
				ClaimToken: "claim-secret",
				State:      agentproto.ResultSucceeded,
			},
		)
	}
	if err == nil {
		err = client.Cancelled(context.Background(), 42, "claim-secret")
	}

	// Then
	if err != nil {
		t.Fatalf("lifecycle mutation: %v", err)
	}
	for _, want := range []string{"/agent/v1/deployments/42/start", "/agent/v1/deployments/42/logs", "/agent/v1/deployments/42/result", "/agent/v1/deployments/42/cancelled"} {
		if got := <-paths; got != want {
			t.Fatalf("request path = %q, want %q", got, want)
		}
	}
}

func TestClient_decodesOnlyItsClaimedEnvelope(t *testing.T) {
	// Given
	client := testClient(
		t,
		"https://agent.example.test",
		testIdentity(t).Fingerprint.String(),
	)
	envelope, err := agentpayload.Seal(
		client.identity.Certificate.Certificate[0],
		42,
		[]byte("payload"),
	)
	if err != nil {
		t.Fatalf("seal envelope: %v", err)
	}

	// When
	decoded, err := client.DecodePayload(
		agentproto.PollResponse{DeploymentID: 42, Payload: string(envelope)},
	)

	// Then
	if err != nil || string(decoded) != "payload" {
		t.Fatalf("decode envelope = %q, %v", decoded, err)
	}
}

func TestClient_stops_on_shutdown(t *testing.T) {
	// Given
	serverIdentity := testIdentity(t)
	server := newTLSServer(
		t,
		serverIdentity,
		func(w http.ResponseWriter, r *http.Request) {
			<-r.Context().Done()
		},
	)
	client := testClient(t, server.URL, serverIdentity.Fingerprint.String())
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() {
		_, err := client.Poll(ctx)
		finished <- err
	}()

	// When
	cancel()
	client.Close()
	err := <-finished

	// Then
	if err == nil {
		t.Fatal("shutdown did not cancel poll")
	}
}

func newTLSServer(
	t *testing.T,
	identity agenttls.Identity,
	handler http.HandlerFunc,
) *httptest.Server {
	t.Helper()
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{identity.Certificate},
		MinVersion:   tls.VersionTLS13,
		ClientAuth:   tls.RequestClientCert,
	}
	server.StartTLS()
	t.Cleanup(server.Close)
	return server
}

func testClient(t *testing.T, serverURL, pin string) *Client {
	t.Helper()
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("secure state directory: %v", err)
	}
	if _, err := agenttls.LoadOrCreate(stateDir, serverURL); err != nil {
		t.Fatalf("create agent identity: %v", err)
	}
	serverPin, err := agenttls.ParseFingerprint(pin)
	if err != nil {
		t.Fatalf("parse server pin: %v", err)
	}
	state, err := agentstate.New(
		serverURL,
		[]agenttls.Fingerprint{serverPin},
		"agent-a",
	)
	if err != nil {
		t.Fatalf("create paired state: %v", err)
	}
	if err := agentstate.NewStore(stateDir).Save(state); err != nil {
		t.Fatalf("save paired state: %v", err)
	}
	client, err := NewPaired(stateDir, "v1")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

func testIdentity(t *testing.T) agenttls.Identity {
	t.Helper()
	identity, err := agenttls.LoadOrCreate(t.TempDir(), "https://127.0.0.1")
	if err != nil {
		t.Fatalf("create identity: %v", err)
	}
	return identity
}

package agentbootstrap_test

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"durpdeploy/internal/agentbootstrap"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
)

func TestBootstrap_AcceptsServerCertOnlyAfterCodeMatch(t *testing.T) {
	// Given
	listener := newServerInitListener(t, time.Now)
	serverIdentity := newServerInitIdentity(t)
	pullEndpoint := testServerInitEndpoint(t)
	wrongCode := testServerInitCode(t, 0)

	// When
	wrongStatus := postServerInit(
		t,
		listener,
		serverIdentity,
		serverInitRequest(t, listener, wrongCode, serverIdentity, pullEndpoint),
	)
	acceptedStatus := postServerInit(
		t,
		listener,
		serverIdentity,
		serverInitRequest(
			t,
			listener,
			listener.Offer().Code,
			serverIdentity,
			pullEndpoint,
		),
	)

	// Then
	if wrongStatus != http.StatusUnauthorized {
		t.Fatalf(
			"wrong-code status = %d, want %d",
			wrongStatus,
			http.StatusUnauthorized,
		)
	}
	if acceptedStatus != http.StatusNoContent {
		t.Fatalf(
			"accepted status = %d, want %d",
			acceptedStatus,
			http.StatusNoContent,
		)
	}
}

func TestBootstrap_RejectsExpiredCode(t *testing.T) {
	// Given
	now := time.Now()
	clock := now
	listener := newServerInitListener(t, func() time.Time { return clock })
	serverIdentity := newServerInitIdentity(t)
	clock = now.Add(10*time.Minute + time.Nanosecond)

	// When
	status := postServerInit(
		t,
		listener,
		serverIdentity,
		serverInitRequest(
			t,
			listener,
			listener.Offer().Code,
			serverIdentity,
			testServerInitEndpoint(t),
		),
	)

	// Then
	if status != http.StatusUnauthorized {
		t.Fatalf(
			"expired status = %d, want %d",
			status,
			http.StatusUnauthorized,
		)
	}
}

func TestBootstrap_RejectsServerCertAfterCommit(t *testing.T) {
	// Given
	listener := newServerInitListener(t, time.Now)
	firstServer := newServerInitIdentity(t)
	secondServer := newServerInitIdentity(t)
	pullEndpoint := testServerInitEndpoint(t)

	// When
	firstStatus := postServerInit(
		t,
		listener,
		firstServer,
		serverInitRequest(
			t,
			listener,
			listener.Offer().Code,
			firstServer,
			pullEndpoint,
		),
	)
	secondStatus := postServerInit(
		t,
		listener,
		secondServer,
		serverInitRequest(
			t,
			listener,
			listener.Offer().Code,
			secondServer,
			pullEndpoint,
		),
	)

	// Then
	if firstStatus != http.StatusNoContent {
		t.Fatalf(
			"first status = %d, want %d",
			firstStatus,
			http.StatusNoContent,
		)
	}
	if secondStatus != http.StatusGone {
		t.Fatalf("replay status = %d, want %d", secondStatus, http.StatusGone)
	}
}

func TestBootstrap_GetDoesNotRevealPairingCode(t *testing.T) {
	// Given
	listener := newServerInitListener(t, time.Now)
	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: &tls.Config{
			MinVersion:         tls.VersionTLS13,
			InsecureSkipVerify: true,
		}},
	}
	request, err := http.NewRequest(
		http.MethodGet,
		listener.Endpoint()+"/agent/v1/bootstrap",
		nil,
	)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}

	// When
	response, err := client.Do(request)
	if err != nil {
		t.Fatalf("GET bootstrap: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf(
			"GET status = %d, want %d",
			response.StatusCode,
			http.StatusNotFound,
		)
	}
}

func TestBootstrap_RejectsUnauthenticatedServerInit(t *testing.T) {
	// Given
	listener := newServerInitListener(t, time.Now)
	serverIdentity := newServerInitIdentity(t)
	body := serverInitRequest(
		t,
		listener,
		listener.Offer().Code,
		serverIdentity,
		testServerInitEndpoint(t),
	)
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		listener.Endpoint()+agentproto.ServerInitPath,
		bytes.NewReader(encoded),
	)
	if err != nil {
		t.Fatalf("create server-init request: %v", err)
	}

	// When
	response, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion:         tls.VersionTLS13,
		InsecureSkipVerify: true,
	}}}).Do(
		request,
	)
	if err != nil {
		t.Fatalf("post unauthenticated server-init: %v", err)
	}
	defer response.Body.Close()

	// Then
	if response.StatusCode != http.StatusUnauthorized {
		t.Fatalf(
			"status = %d, want %d",
			response.StatusCode,
			http.StatusUnauthorized,
		)
	}
	if status := postServerInit(
		t,
		listener,
		serverIdentity,
		body,
	); status != http.StatusNoContent {
		t.Fatalf(
			"valid retry status = %d, want %d",
			status,
			http.StatusNoContent,
		)
	}
}

func TestBootstrap_RejectsMismatchedServerCertificate(t *testing.T) {
	// Given
	listener := newServerInitListener(t, time.Now)
	expectedServer := newServerInitIdentity(t)
	otherServer := newServerInitIdentity(t)
	body := serverInitRequest(
		t,
		listener,
		listener.Offer().Code,
		expectedServer,
		testServerInitEndpoint(t),
	)

	// When
	status := postServerInit(t, listener, otherServer, body)

	// Then
	if status != http.StatusUnauthorized {
		t.Fatalf(
			"mismatched certificate status = %d, want %d",
			status,
			http.StatusUnauthorized,
		)
	}
	if status := postServerInit(
		t,
		listener,
		expectedServer,
		body,
	); status != http.StatusNoContent {
		t.Fatalf(
			"valid retry status = %d, want %d",
			status,
			http.StatusNoContent,
		)
	}
}

func newServerInitListener(
	t *testing.T,
	now func() time.Time,
) *agentbootstrap.Listener {
	t.Helper()
	stateDir := t.TempDir()
	listener, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: stateDir, ListenAddr: "127.0.0.1:0", Now: now,
	})
	if err != nil {
		t.Fatalf("start listener: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(
			context.Background(),
			time.Second,
		)
		defer cancel()
		if shutdownErr := listener.Shutdown(shutdownCtx); shutdownErr != nil {
			t.Errorf("shutdown listener: %v", shutdownErr)
		}
	})
	return listener
}

func newServerInitIdentity(t *testing.T) agenttls.Identity {
	t.Helper()
	identity, err := agenttls.LoadOrCreate(t.TempDir(), "https://127.0.0.1")
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	return identity
}

func testServerInitEndpoint(t *testing.T) agentproto.PullEndpoint {
	t.Helper()
	endpoint, err := agentproto.ParsePullEndpoint("https://server.example.test")
	if err != nil {
		t.Fatalf("parse pull endpoint: %v", err)
	}
	return endpoint
}

func testServerInitCode(t *testing.T, value byte) agentproto.PairingCode {
	t.Helper()
	code, err := agentproto.ParsePairingCode(
		base64.RawURLEncoding.EncodeToString(bytes.Repeat(
			[]byte{value},
			agentproto.PairingCodeEntropyBytes,
		)),
	)
	if err != nil {
		t.Fatalf("parse test pairing code: %v", err)
	}
	return code
}

func serverInitRequest(
	t *testing.T,
	listener *agentbootstrap.Listener,
	code agentproto.PairingCode,
	identity agenttls.Identity,
	pullEndpoint agentproto.PullEndpoint,
) agentproto.PairRequest {
	t.Helper()
	serverPin, err := agentproto.ParseSHA256Pin(identity.Fingerprint.String())
	if err != nil {
		t.Fatalf("parse server pin: %v", err)
	}
	return agentproto.PairRequest{
		ProtocolEnvelope: agentproto.ProtocolEnvelope{
			Protocol: agentproto.AgentV1,
		},
		PairingCode: code, AgentPin: listener.Offer().AgentPin, ServerPin: serverPin,
		PullEndpoint: pullEndpoint, AgentID: "agent-under-test",
	}
}

func postServerInit(
	t *testing.T,
	listener *agentbootstrap.Listener,
	identity agenttls.Identity,
	body agentproto.PairRequest,
) int {
	t.Helper()
	pin, err := agenttls.ParseFingerprint(listener.Offer().AgentPin.String())
	if err != nil {
		t.Fatalf("parse agent pin: %v", err)
	}
	config, err := agenttls.NewPairingBootstrapClientConfig(
		listener.Endpoint(),
		pin,
	)
	if err != nil {
		t.Fatalf("new pinned TLS config: %v", err)
	}
	config.Certificates = []tls.Certificate{identity.Certificate}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	request, err := http.NewRequest(
		http.MethodPost,
		listener.Endpoint()+agentproto.ServerInitPath,
		bytes.NewReader(encoded),
	)
	if err != nil {
		t.Fatalf("create server-init request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{Transport: &http.Transport{TLSClientConfig: config}}).Do(
		request,
	)
	if err != nil {
		t.Fatalf("post server-init: %v", err)
	}
	defer response.Body.Close()
	return response.StatusCode
}

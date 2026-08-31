package agentclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/agenttls"
)

func TestNewPaired_restartsWithoutManualServerConfiguration(t *testing.T) {
	// Given
	serverIdentity := testIdentity(t)
	versions := make(chan string, 2)
	server := newTLSServer(t, serverIdentity, func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == agentproto.PollPath {
			var poll agentproto.PollRequest
			if err := json.NewDecoder(request.Body).Decode(&poll); err != nil {
				t.Fatalf("decode poll request: %v", err)
			}
			versions <- string(poll.AgentVersion)
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("secure state directory: %v", err)
	}
	if _, err := agenttls.LoadOrCreate(
		stateDir,
		"https://127.0.0.1",
	); err != nil {
		t.Fatalf("create paired agent identity: %v", err)
	}
	paired, err := agentstate.New(
		server.URL,
		[]agenttls.Fingerprint{serverIdentity.Fingerprint},
		"agent-paired",
	)
	if err != nil {
		t.Fatalf("create paired state: %v", err)
	}
	paired.AgentVersion = "persisted"
	if err := agentstate.NewStore(stateDir).Save(paired); err != nil {
		t.Fatalf("save paired state: %v", err)
	}

	// When
	first, err := NewPaired(stateDir, "")
	if err != nil {
		t.Fatalf("start paired client: %v", err)
	}
	defer first.Close()
	_, err = first.Poll(context.Background())
	if err != nil {
		t.Fatalf("first paired poll: %v", err)
	}
	first.Close()
	second, err := NewPaired(stateDir, "")
	if err != nil {
		t.Fatalf("restart paired client: %v", err)
	}
	defer second.Close()
	_, err = second.Poll(context.Background())

	// Then
	if err != nil {
		t.Fatalf("restarted paired poll: %v", err)
	}
	for _, want := range []string{"persisted", "persisted"} {
		if got := <-versions; got != want {
			t.Fatalf("poll version = %q, want %q", got, want)
		}
	}
}

func TestNewPaired_usesLegacyFallbackVersionWhenStateAndRuntimeMissing(
	t *testing.T,
) {
	// Given
	serverIdentity := testIdentity(t)
	versions := make(chan string, 1)
	server := newTLSServer(t, serverIdentity, func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path == agentproto.PollPath {
			var poll agentproto.PollRequest
			if err := json.NewDecoder(request.Body).Decode(&poll); err != nil {
				t.Fatalf("decode poll request: %v", err)
			}
			versions <- string(poll.AgentVersion)
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("secure state directory: %v", err)
	}
	if _, err := agenttls.LoadOrCreate(
		stateDir,
		"https://127.0.0.1",
	); err != nil {
		t.Fatalf("create paired agent identity: %v", err)
	}
	contents, err := json.Marshal(struct {
		ServerURL  string   `json:"server_url"`
		ServerPins []string `json:"server_pins"`
		AgentID    string   `json:"agent_id"`
	}{
		ServerURL:  server.URL,
		ServerPins: []string{serverIdentity.Fingerprint.String()},
		AgentID:    "agent-legacy",
	})
	if err != nil {
		t.Fatalf("marshal legacy paired state: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(stateDir, agentstate.FileName),
		contents,
		0o600,
	); err != nil {
		t.Fatalf("write legacy paired state: %v", err)
	}

	// When
	client, err := NewPaired(stateDir, "")
	if err != nil {
		t.Fatalf("start paired client: %v", err)
	}
	defer client.Close()
	if _, err := client.Poll(context.Background()); err != nil {
		t.Fatalf("poll legacy paired client: %v", err)
	}

	// Then
	if got := <-versions; got != string(legacyAgentVersion) {
		t.Fatalf("poll version = %q, want %q", got, legacyAgentVersion)
	}
}

func TestNewPaired_LoadsPromotedPinAfterRestart(t *testing.T) {
	// Given
	current := testIdentity(t)
	pending := testIdentity(t)
	client := testClient(
		t,
		"https://agent.example.test",
		current.Fingerprint.String(),
	)
	if err := client.stagePins([]agentproto.CertificateFingerprint{
		agentproto.CertificateFingerprint(current.Fingerprint.String()),
		agentproto.CertificateFingerprint(pending.Fingerprint.String()),
	}); err != nil {
		t.Fatalf("stage server pins: %v", err)
	}
	pendingCertificate, err := x509.ParseCertificate(
		pending.Certificate.Certificate[0],
	)
	if err != nil {
		t.Fatalf("parse pending server certificate: %v", err)
	}

	// When
	if err := client.promotePin(&tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{pendingCertificate},
	}); err != nil {
		t.Fatalf("promote server pin: %v", err)
	}
	client.Close()
	restarted, err := NewPaired(client.stateDir, "v1")
	if err != nil {
		t.Fatalf("restart paired client: %v", err)
	}
	defer restarted.Close()

	// Then
	if len(restarted.pins) != 1 || restarted.pins[0] != pending.Fingerprint {
		t.Fatalf("restarted pins = %v, want promoted pin", restarted.pins)
	}
	if _, err := os.Stat(
		filepath.Join(client.stateDir, "server-pins.json"),
	); !os.IsNotExist(
		err,
	) {
		t.Fatalf("orphan pin state stat error = %v, want not exist", err)
	}
}

func TestNewPaired_LoadsStagedPinsAfterRestart(t *testing.T) {
	// Given
	current := testIdentity(t)
	pending := testIdentity(t)
	client := testClient(
		t,
		"https://agent.example.test",
		current.Fingerprint.String(),
	)
	if err := client.stagePins([]agentproto.CertificateFingerprint{
		agentproto.CertificateFingerprint(current.Fingerprint.String()),
		agentproto.CertificateFingerprint(pending.Fingerprint.String()),
	}); err != nil {
		t.Fatalf("stage server pins: %v", err)
	}

	// When
	restarted, err := NewPaired(client.stateDir, "v1")
	if err != nil {
		t.Fatalf("restart paired client: %v", err)
	}
	defer restarted.Close()

	// Then
	if len(restarted.pins) != 2 || restarted.pins[0] != current.Fingerprint ||
		restarted.pins[1] != pending.Fingerprint {
		t.Fatalf(
			"restarted pins = %v, want current and pending pins",
			restarted.pins,
		)
	}
}

func TestNewPaired_RejectsStateWithDuplicatedPins(t *testing.T) {
	// Given
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("secure state directory: %v", err)
	}
	contents := []byte(
		`{"server_url":"https://agent.example.test","server_pins":["0000000000000000000000000000000000000000000000000000000000000000","0000000000000000000000000000000000000000000000000000000000000000"],"agent_id":"agent-duplicate"}`,
	)
	if err := os.WriteFile(
		filepath.Join(stateDir, agentstate.FileName),
		contents,
		0o600,
	); err != nil {
		t.Fatalf("write duplicated paired state: %v", err)
	}

	// When
	_, err := NewPaired(stateDir, "v1")

	// Then
	if !errors.Is(err, agentstate.ErrRePairRequired) {
		t.Fatalf("NewPaired error = %v, want re-pair requirement", err)
	}
}

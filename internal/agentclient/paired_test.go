package agentclient

import (
	"context"
	"net/http"
	"os"
	"testing"

	"durpdeploy/internal/agentstate"
	"durpdeploy/internal/agenttls"
)

func TestNewPaired_restartsWithoutManualServerConfiguration(t *testing.T) {
	// Given
	serverIdentity := testIdentity(t)
	server := newTLSServer(t, serverIdentity, func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
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
	if err := agentstate.NewStore(stateDir).Save(paired); err != nil {
		t.Fatalf("save paired state: %v", err)
	}

	// When
	first, err := NewPaired(stateDir, "test")
	if err != nil {
		t.Fatalf("start paired client: %v", err)
	}
	defer first.Close()
	_, err = first.Poll(context.Background())
	if err != nil {
		t.Fatalf("first paired poll: %v", err)
	}
	first.Close()
	second, err := NewPaired(stateDir, "test")
	if err != nil {
		t.Fatalf("restart paired client: %v", err)
	}
	defer second.Close()
	_, err = second.Poll(context.Background())

	// Then
	if err != nil {
		t.Fatalf("restarted paired poll: %v", err)
	}
}

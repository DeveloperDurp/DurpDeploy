package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestRunDevAgentIdentity_provisionsAndPreservesIdentity(t *testing.T) {
	// Given: a configured local listener with no identity files yet.
	dir := filepath.Join(t.TempDir(), "server-identity")
	t.Setenv("DURPDEPLOY_AGENT_PUBLIC_URL", "https://localhost")
	t.Setenv("DURPDEPLOY_AGENT_IDENTITY_DIR", dir)

	// When: the development identity preparation runs twice.
	if code := runDevAgentIdentity(); code != 0 {
		t.Fatalf("first identity preparation exit code = %d, want 0", code)
	}
	keyPath := filepath.Join(dir, "identity.key")
	firstKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read generated identity key: %v", err)
	}
	if _, err := agenttls.LoadExisting(dir); err != nil {
		t.Fatalf("load generated identity: %v", err)
	}
	if code := runDevAgentIdentity(); code != 0 {
		t.Fatalf("second identity preparation exit code = %d, want 0", code)
	}

	// Then: the identity is valid and the existing private key is preserved.
	secondKey, err := os.ReadFile(keyPath)
	if err != nil {
		t.Fatalf("read preserved identity key: %v", err)
	}
	if !bytes.Equal(firstKey, secondKey) {
		t.Fatal("second identity preparation replaced the private key")
	}
}

package main

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestServerAgentChild(t *testing.T) {
	if os.Getenv("TASK4_CHILD") == "1" {
		runServer()
	}
}

func TestServerMissingAgentIdentityFailsClosed(t *testing.T) {
	testAgentChildFailure(t, "missing")
}

func TestServerInvalidAgentIdentityFailsClosed(t *testing.T) {
	testAgentChildFailure(t, "invalid")
}

func testAgentChildFailure(t *testing.T, material string) {
	t.Helper()
	dir := t.TempDir()
	if material == "invalid" {
		for _, name := range []string{"identity.crt", "identity.key"} {
			if err := os.WriteFile(filepath.Join(dir, name), []byte("invalid"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	port, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := port.Addr().String()
	port.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(
		ctx,
		os.Args[0],
		"-test.run=^TestServerAgentChild$",
	)
	cmd.Dir = t.TempDir()
	cmd.Env = append(os.Environ(), "TASK4_CHILD=1",
		"DURPDEPLOY_AGENT_LISTEN_ADDR="+addr,
		"DURPDEPLOY_AGENT_PUBLIC_URL=https://localhost",
		"DURPDEPLOY_AGENT_IDENTITY_DIR="+dir)
	output, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil {
		t.Fatalf("child did not exit 1: %v", err)
	}
	if !strings.Contains(string(output), "DURPDEPLOY_AGENT_IDENTITY_DIR") ||
		!strings.Contains(string(output), "identity.crt and identity.key") {
		t.Fatalf("missing actionable diagnostic: %s", output)
	}
	rebound, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("agent port remains bound: %v", err)
	}
	rebound.Close()
	entries, err := os.ReadDir(cmd.Dir)
	if err != nil || len(entries) != 0 {
		t.Fatal("startup touched child working directory before validation")
	}
	t.Logf(
		"%s identity: child exit=1; port %s rebound; no database created; diagnostic=%s",
		material,
		addr,
		output,
	)
}

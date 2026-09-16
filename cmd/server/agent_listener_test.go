package main

import (
	"context"
	"net"
	"testing"
	"time"

	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestServerAgentIdentityConfigRejectsInvalidAddress(t *testing.T) {
	// Given: an explicit listener address with a non-numeric port.
	t.Setenv("DURPDEPLOY_AGENT_LISTEN_ADDR", "localhost:invalid")
	t.Setenv("DURPDEPLOY_AGENT_PUBLIC_URL", "https://localhost")
	t.Setenv("DURPDEPLOY_AGENT_IDENTITY_DIR", t.TempDir())

	// When: server startup loads the listener configuration.
	_, err := loadAgentListenerConfig()

	// Then: invalid explicit settings are still rejected.
	if err == nil {
		t.Fatal("invalid configuration accepted")
	}
}

func TestRuntimeAgentShutdownRestart(t *testing.T) {
	dir := t.TempDir()
	if _, err := agenttls.LoadOrCreate(dir, "https://localhost"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DURPDEPLOY_AGENT_LISTEN_ADDR", "127.0.0.1:0")
	t.Setenv("DURPDEPLOY_AGENT_PUBLIC_URL", "https://localhost")
	t.Setenv("DURPDEPLOY_AGENT_IDENTITY_DIR", dir)
	config, err := loadAgentListenerConfig()
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	if got := config.pullEndpoint.String(); got != "https://localhost" {
		t.Fatalf("pull endpoint=%q", got)
	}
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	dispatcher := dispatch.New(repo)
	for range 2 {
		listener, err := startAgentListener(context.Background(), config,
			agentListenerDependencies{repo: repo, dispatcher: dispatcher})
		if err != nil {
			t.Fatal(err)
		}
		addr := listener.listener.Addr().String()
		occupied := config
		occupied.addr = addr
		if extra, err := startAgentListener(
			context.Background(),
			occupied,
			agentListenerDependencies{
				repo:       repo,
				dispatcher: dispatcher,
			},
		); err == nil {
			extra.shutdown(context.Background())
			t.Fatal("occupied port accepted")
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		err = listener.shutdown(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		select {
		case <-listener.maintain:
		default:
			t.Fatal("maintenance did not terminate")
		}
		rebound, err := net.Listen("tcp", addr)
		if err != nil {
			t.Fatalf("listener leaked: %v", err)
		}
		rebound.Close()
		t.Logf("shutdown joined serve/maintenance; rebound %s", addr)
	}
}

func TestServerAgentListenerDefaultsToActive(t *testing.T) {
	// Given: no agent listener configuration is present.
	t.Chdir(t.TempDir())
	for _, key := range []string{
		"DURPDEPLOY_AGENT_LISTEN_ADDR",
		"DURPDEPLOY_AGENT_PUBLIC_URL", "DURPDEPLOY_AGENT_IDENTITY_DIR",
	} {
		t.Setenv(key, "")
	}

	// When: server startup loads the listener configuration.
	config, err := loadAgentListenerConfig()

	// Then: the default listener and a persistent identity are ready.
	if err != nil {
		t.Fatal(err)
	}
	if config.addr != "0.0.0.0:10943" {
		t.Fatalf("default listener address = %q", config.addr)
	}
	if got := config.pullEndpoint.String(); got != "https://localhost:10943" {
		t.Fatalf("default public URL = %q", got)
	}
	if _, err := agenttls.LoadExisting(".agent-identity"); err != nil {
		t.Fatalf("load default identity: %v", err)
	}
}

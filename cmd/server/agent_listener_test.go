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

func TestServerAgentIdentityConfig(t *testing.T) {
	for _, name := range []string{"missing", "empty", "invalid address"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DURPDEPLOY_AGENT_LISTEN_ADDR", "127.0.0.1:0")
			t.Setenv("DURPDEPLOY_AGENT_PUBLIC_URL", "https://localhost")
			t.Setenv("DURPDEPLOY_AGENT_IDENTITY_DIR", t.TempDir())
			if name == "missing" {
				t.Setenv("DURPDEPLOY_AGENT_IDENTITY_DIR", "")
			}
			if name == "invalid address" {
				dir := t.TempDir()
				if _, err := agenttls.LoadOrCreate(dir, "https://localhost"); err != nil {
					t.Fatal(err)
				}
				t.Setenv("DURPDEPLOY_AGENT_IDENTITY_DIR", dir)
				_, enabled, err := loadAgentListenerConfig()
				if err != nil || !enabled {
					t.Fatalf("valid control: enabled=%v err=%v", enabled, err)
				}
				t.Setenv("DURPDEPLOY_AGENT_LISTEN_ADDR", "localhost:invalid")
			}
			_, _, err := loadAgentListenerConfig()
			if err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
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
	config, enabled, err := loadAgentListenerConfig()
	if err != nil || !enabled {
		t.Fatalf("config: enabled=%v err=%v", enabled, err)
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
		if extra, err := startAgentListener(context.Background(), occupied,
			agentListenerDependencies{repo: repo, dispatcher: dispatcher}); err == nil {
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

func TestServerAgentListenerDisabled(t *testing.T) {
	for _, key := range []string{
		"DURPDEPLOY_AGENT_LISTEN_ADDR",
		"DURPDEPLOY_AGENT_PUBLIC_URL", "DURPDEPLOY_AGENT_IDENTITY_DIR",
	} {
		t.Setenv(key, "")
	}
	_, enabled, err := loadAgentListenerConfig()
	if err != nil || enabled {
		t.Fatalf("unconfigured listener: enabled=%v err=%v", enabled, err)
	}
}

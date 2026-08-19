package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	"durpdeploy/internal/agentclient"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

func TestLoadAgentListenerConfig_defaultsOffAndRejectsPartialConfig(
	t *testing.T,
) {
	for _, name := range []string{
		"DURPDEPLOY_AGENT_LISTEN_ADDR", "DURPDEPLOY_AGENT_PUBLIC_URL",
		"DURPDEPLOY_AGENT_IDENTITY_DIR", "DURPDEPLOY_AGENT_PENDING_FINGERPRINT",
	} {
		t.Setenv(name, "")
	}
	if _, enabled, err := loadAgentListenerConfig(); err != nil || enabled {
		t.Fatalf("unset agent listener = enabled %t, err %v", enabled, err)
	}
	t.Setenv("DURPDEPLOY_AGENT_LISTEN_ADDR", "127.0.0.1:0")
	if _, _, err := loadAgentListenerConfig(); err == nil {
		t.Fatal("partial agent listener config succeeded")
	}
	t.Setenv("DURPDEPLOY_AGENT_PUBLIC_URL", "https://agents.example.test")
	t.Setenv("DURPDEPLOY_AGENT_IDENTITY_DIR", t.TempDir())
	t.Setenv("DURPDEPLOY_AGENT_PENDING_FINGERPRINT", "invalid")
	if _, _, err := loadAgentListenerConfig(); err == nil {
		t.Fatal("invalid pending server pin succeeded")
	}
}

func TestAgentListener_servesPinnedTLSAndStops(t *testing.T) {
	conn, err := migrate.Run(tempDSN(t))
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("secret box: %v", err)
	}
	listener, err := startAgentListener(agentListenerConfig{
		addr: "127.0.0.1:0", publicURL: "https://127.0.0.1",
		identityDir: t.TempDir(),
	}, agentListenerDependencies{
		repo: repository.New(conn), box: box, broker: runner.NewLogBroker(),
		bus: events.NewBus(repository.New(conn)),
	})
	if err != nil {
		t.Fatalf("start agent listener: %v", err)
	}
	address := listener.listener.Addr().(*net.TCPAddr)
	clientTLS, err := agenttls.NewClientConfig(
		fmt.Sprintf("https://127.0.0.1:%d", address.Port),
		[]agenttls.Fingerprint{listener.identity.Fingerprint},
	)
	if err != nil {
		t.Fatalf("client TLS config: %v", err)
	}
	connection, err := (&tls.Dialer{Config: clientTLS}).DialContext(
		context.Background(), "tcp", address.String(),
	)
	if err != nil {
		t.Fatalf("dial pinned listener: %v", err)
	}
	_ = connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := listener.shutdown(ctx); err != nil {
		t.Fatalf("shutdown agent listener: %v", err)
	}
}

func TestAgentListener_enrollsPinnedAgent(t *testing.T) {
	conn, err := migrate.Run(tempDSN(t))
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer conn.Close()
	repo := repository.New(conn)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("secret box: %v", err)
	}
	listener, err := startAgentListener(agentListenerConfig{
		addr: "127.0.0.1:0", publicURL: "https://127.0.0.1",
		identityDir: t.TempDir(),
	}, agentListenerDependencies{
		repo: repo, box: box, broker: runner.NewLogBroker(), bus: events.NewBus(repo),
	})
	if err != nil {
		t.Fatalf("start agent listener: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	defer func() {
		if shutdownErr := listener.shutdown(ctx); shutdownErr != nil {
			t.Errorf("shutdown agent listener: %v", shutdownErr)
		}
	}()
	if _, err := repo.Queries.CreatePendingAgent(
		ctx,
		db.CreatePendingAgentParams{
			ID: "live-agent", Name: "live agent",
		},
	); err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
	token := "test-enrollment-token"
	hash := sha256.Sum256([]byte(token))
	if err := repo.Queries.CreateAgentEnrollmentToken(ctx,
		db.CreateAgentEnrollmentTokenParams{
			TokenHash: hash[:], TokenPrefix: "test", AgentID: "live-agent",
			ExpiresAt: time.Now().Add(time.Minute).Unix(),
		}); err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
	address := listener.listener.Addr().(*net.TCPAddr)
	stateDir := t.TempDir()
	if err := os.Chmod(stateDir, 0o700); err != nil {
		t.Fatalf("chmod agent state directory: %v", err)
	}
	client, err := agentclient.New(agentclient.Config{
		ServerURL:         fmt.Sprintf("https://127.0.0.1:%d", address.Port),
		ServerFingerprint: listener.identity.Fingerprint.String(),
		StateDir:          stateDir, EnrollmentToken: token,
		AgentID: agentproto.AgentID("live-agent"), Name: "live agent",
		AgentVersion: agentproto.AgentVersion(
			"test",
		), Protocol: string(agentproto.AgentV1),
	})
	if err != nil {
		t.Fatalf("new agent client: %v", err)
	}
	defer client.Close()
	if err := client.Enroll(ctx); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	agent, err := repo.Queries.GetAgent(ctx, "live-agent")
	if err != nil {
		t.Fatalf("get enrolled agent: %v", err)
	}
	if agent.Status != "active" {
		t.Fatalf("agent status = %q, want active", agent.Status)
	}
}

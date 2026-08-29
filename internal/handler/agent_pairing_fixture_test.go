package handler_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/agentbootstrap"
	"durpdeploy/internal/agentpairing"
	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/server"
)

type agentPairingTestEnv struct {
	bootstrap    *agentbootstrap.Listener
	pairer       *agentpairing.Server
	repo         *repository.Repository
	server       *httptest.Server
	session      *authedSession
	stateDir     string
	bootstrapURL string
	code         string
	codeHash     agentproto.PairingCodeHash
	agentPin     string
}

type agentPairingTestConfig struct {
	pairingTTL      time.Duration
	confirmationTTL time.Duration
	afterPairCommit func(http.ResponseWriter) bool
}

func newAgentPairingTestEnv(t *testing.T) *agentPairingTestEnv {
	return newAgentPairingTestEnvWithTTL(t, 0)
}

func newAgentPairingTestEnvWithTTL(
	t *testing.T,
	pairingTTL time.Duration,
) *agentPairingTestEnv {
	return newAgentPairingTestEnvWithConfig(t, agentPairingTestConfig{
		pairingTTL: pairingTTL,
	})
}

func newAgentPairingTestEnvWithTTLs(
	t *testing.T,
	pairingTTL time.Duration,
	confirmationTTL time.Duration,
) *agentPairingTestEnv {
	return newAgentPairingTestEnvWithConfig(t, agentPairingTestConfig{
		pairingTTL: pairingTTL, confirmationTTL: confirmationTTL,
	})
}

func newAgentPairingTestEnvWithConfig(
	t *testing.T,
	config agentPairingTestConfig,
) *agentPairingTestEnv {
	t.Helper()
	stateDir := t.TempDir()
	bootstrap, err := agentbootstrap.Start(agentbootstrap.Config{
		StateDir: stateDir, ListenAddr: "127.0.0.1:0", PairingTTL: config.pairingTTL,
		AfterPairCommit: config.afterPairCommit,
	})
	if err != nil {
		t.Fatalf("start bootstrap agent: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if shutdownErr := bootstrap.Shutdown(shutdownCtx); shutdownErr != nil {
			t.Errorf("shutdown bootstrap agent: %v", shutdownErr)
		}
	})
	serverIdentity, err := agenttls.LoadOrCreate(t.TempDir(), "https://127.0.0.1")
	if err != nil {
		t.Fatalf("create server identity: %v", err)
	}
	pullEndpoint, err := agentproto.ParsePullEndpoint("https://127.0.0.1:10944")
	if err != nil {
		t.Fatalf("parse pull endpoint: %v", err)
	}
	pairer, err := agentpairing.NewServer(pullEndpoint, serverIdentity)
	if err != nil {
		t.Fatalf("create pairer: %v", err)
	}
	repo := repository.New(newHandlerTestDatabase(t))
	rnr := runner.New(repo, runner.NewLogBroker())
	h := httptest.NewServer(server.NewRouterWithAgentPairerAndConfirmationTTL(
		repo,
		rnr,
		nil,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(repo),
		pairer,
		config.confirmationTTL,
	))
	t.Cleanup(h.Close)
	session := seedSession(t, repo, h.URL, "admin")
	return &agentPairingTestEnv{
		bootstrap:    bootstrap,
		pairer:       pairer,
		repo:         repo,
		server:       h,
		session:      session,
		stateDir:     stateDir,
		bootstrapURL: bootstrap.Endpoint(),
		code:         pairingCodeText(t, bootstrap.Offer().Code),
		codeHash:     bootstrap.Offer().Code.Hash(),
		agentPin:     bootstrap.Offer().AgentPin.String(),
	}
}

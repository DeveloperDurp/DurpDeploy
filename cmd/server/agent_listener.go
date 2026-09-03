package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type agentListenerConfig struct {
	addr        string
	publicURL   string
	identityDir string
	pendingPin  *agenttls.Fingerprint
}

func loadAgentListenerConfig() (agentListenerConfig, bool, error) {
	pendingPin := os.Getenv("DURPDEPLOY_AGENT_PENDING_FINGERPRINT")
	config := agentListenerConfig{
		addr:        os.Getenv("DURPDEPLOY_AGENT_LISTEN_ADDR"),
		publicURL:   os.Getenv("DURPDEPLOY_AGENT_PUBLIC_URL"),
		identityDir: os.Getenv("DURPDEPLOY_AGENT_IDENTITY_DIR"),
	}
	if config.addr == "" && config.publicURL == "" &&
		config.identityDir == "" &&
		pendingPin == "" {
		return agentListenerConfig{}, false, nil
	}
	if config.addr == "" || config.publicURL == "" || config.identityDir == "" {
		return agentListenerConfig{}, false, errors.New(
			"agent listener requires DURPDEPLOY_AGENT_LISTEN_ADDR, " +
				"DURPDEPLOY_AGENT_PUBLIC_URL, and DURPDEPLOY_AGENT_IDENTITY_DIR",
		)
	}
	if pendingPin != "" {
		parsed, err := agenttls.ParseFingerprint(pendingPin)
		if err != nil {
			return agentListenerConfig{}, false, fmt.Errorf(
				"parse pending server pin: %w",
				err,
			)
		}
		config.pendingPin = &parsed
	}
	return config, true, nil
}

type agentListener struct {
	server   *http.Server
	listener net.Listener
	done     chan error
	identity agenttls.Identity
	agents   *agentserver.Server
	maintain chan struct{}
}

type agentListenerDependencies struct {
	repo   *repository.Repository
	box    *secret.Box
	broker *runner.LogBroker
	bus    *events.Bus
}

func startAgentListener(
	config agentListenerConfig,
	dependencies agentListenerDependencies,
) (*agentListener, error) {
	identity, err := agenttls.LoadOrCreate(config.identityDir, config.publicURL)
	if err != nil {
		return nil, fmt.Errorf("load agent listener identity: %w", err)
	}
	if config.pendingPin != nil && *config.pendingPin == identity.Fingerprint {
		return nil, errors.New(
			"pending server pin must differ from current identity",
		)
	}
	handler, err := agentserver.New(agentserver.Config{
		Repository: dependencies.repo, Identity: identity, Box: dependencies.box,
		Broker: dependencies.broker, Events: dependencies.bus,
		PendingServerPin: config.pendingPin,
	})
	if err != nil {
		return nil, fmt.Errorf("create agent listener: %w", err)
	}
	listener, err := net.Listen("tcp", config.addr)
	if err != nil {
		return nil, fmt.Errorf("listen for agents: %w", err)
	}
	result := &agentListener{
		server:   &http.Server{Handler: handler.Handler()},
		listener: listener,
		done:     make(chan error, 1),
		identity: identity,
		agents:   handler,
	}
	go func() {
		result.done <- result.server.Serve(tls.NewListener(listener, handler.TLSConfig()))
	}()
	return result, nil
}

func (listener *agentListener) startMaintenance(ctx context.Context) {
	listener.maintain = make(chan struct{})
	go func() {
		defer close(listener.maintain)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			if err := listener.agents.Maintain(ctx); err != nil {
				slog.Error("agent maintenance failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}

func (listener *agentListener) shutdown(ctx context.Context) error {
	if listener.maintain != nil {
		select {
		case <-listener.maintain:
		case <-ctx.Done():
			return fmt.Errorf("stop agent maintenance: %w", ctx.Err())
		}
	}
	if err := listener.server.Shutdown(ctx); err != nil {
		return fmt.Errorf("shutdown agent listener: %w", err)
	}
	if err := <-listener.done; !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("serve agent listener: %w", err)
	}
	return nil
}

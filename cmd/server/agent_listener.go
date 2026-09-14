package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"durpdeploy/internal/agentserver"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/server"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type agentListenerConfig struct {
	addr     string
	identity agenttls.Identity
}

func loadAgentListenerConfig() (agentListenerConfig, bool, error) {
	addr := os.Getenv("DURPDEPLOY_AGENT_LISTEN_ADDR")
	publicURL := os.Getenv("DURPDEPLOY_AGENT_PUBLIC_URL")
	dir := os.Getenv("DURPDEPLOY_AGENT_IDENTITY_DIR")
	if addr == "" && publicURL == "" && dir == "" {
		return agentListenerConfig{}, false, nil
	}
	if addr == "" || publicURL == "" || dir == "" {
		return agentListenerConfig{}, false, errors.New(
			"agent listener requires DURPDEPLOY_AGENT_LISTEN_ADDR, DURPDEPLOY_AGENT_PUBLIC_URL and DURPDEPLOY_AGENT_IDENTITY_DIR containing identity.crt and identity.key",
		)
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return agentListenerConfig{}, false, fmt.Errorf(
			"DURPDEPLOY_AGENT_LISTEN_ADDR: %w",
			err,
		)
	}
	portNumber, err := strconv.Atoi(port)
	if err != nil || portNumber < 0 || portNumber > 65535 {
		return agentListenerConfig{}, false, errors.New(
			"DURPDEPLOY_AGENT_LISTEN_ADDR requires a numeric port from 0 to 65535",
		)
	}
	u, err := url.Parse(publicURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" ||
		u.User != nil ||
		u.RawQuery != "" ||
		u.ForceQuery ||
		u.Fragment != "" ||
		(u.Path != "" && u.Path != "/") {
		return agentListenerConfig{}, false, errors.New(
			"DURPDEPLOY_AGENT_PUBLIC_URL must be an HTTPS origin",
		)
	}
	if u.Port() != "" {
		publicPort, err := strconv.Atoi(u.Port())
		if err != nil || publicPort < 1 || publicPort > 65535 {
			return agentListenerConfig{}, false, errors.New(
				"DURPDEPLOY_AGENT_PUBLIC_URL requires a port from 1 to 65535",
			)
		}
	}
	identity, err := agenttls.LoadExisting(dir)
	if err != nil {
		return agentListenerConfig{}, false, fmt.Errorf(
			"DURPDEPLOY_AGENT_IDENTITY_DIR: provision valid identity.crt and identity.key: %w",
			err,
		)
	}
	cert, err := x509.ParseCertificate(identity.Certificate.Certificate[0])
	if err != nil {
		return agentListenerConfig{}, false, err
	}
	if err := cert.VerifyHostname(u.Hostname()); err != nil {
		return agentListenerConfig{}, false, fmt.Errorf(
			"agent public URL identity mismatch: %w",
			err,
		)
	}
	return agentListenerConfig{addr: addr, identity: identity}, true, nil
}

type agentListenerDependencies struct {
	repo       *repository.Repository
	dispatcher *dispatch.Dispatcher
}

type agentListener struct {
	server   *http.Server
	listener net.Listener
	done     chan error
	maintain chan struct{}
	cancel   context.CancelFunc
}

func startAgentListener(
	ctx context.Context,
	config agentListenerConfig,
	deps agentListenerDependencies,
) (*agentListener, error) {
	agents, err := agentserver.New(agentserver.Config{
		Repository: deps.repo, Dispatcher: deps.dispatcher, Identity: config.identity,
	})
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", config.addr)
	if err != nil {
		return nil, fmt.Errorf("DURPDEPLOY_AGENT_LISTEN_ADDR: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	result := &agentListener{
		server: &http.Server{
			Handler:           server.NewAgentRouter(agents),
			ReadHeaderTimeout: 5 * time.Second, IdleTimeout: time.Minute,
			BaseContext: func(net.Listener) context.Context { return ctx },
		},
		listener: ln, done: make(chan error, 1),
		maintain: make(chan struct{}), cancel: cancel,
	}
	go func() {
		result.done <- result.server.Serve(tls.NewListener(ln, agents.TLSConfig(ctx)))
		close(result.done)
	}()
	go func() {
		defer close(result.maintain)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			if err := agents.Maintain(ctx); err != nil && ctx.Err() == nil {
				slog.Error("agent maintenance failed", "err", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return result, nil
}

func (l *agentListener) shutdown(ctx context.Context) error {
	l.cancel()
	err := l.server.Shutdown(ctx)
	if err != nil {
		err = errors.Join(err, l.server.Close())
	}
	select {
	case <-l.maintain:
	case <-ctx.Done():
		return errors.Join(err, ctx.Err())
	}
	serveErr := <-l.done
	if !errors.Is(serveErr, http.ErrServerClosed) {
		err = errors.Join(err, serveErr)
	}
	return err
}

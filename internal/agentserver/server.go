// Package agentserver owns the dedicated pinned mutual-TLS agent boundary.
package agentserver

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type Config struct {
	Repository *repository.Repository
	Dispatcher *dispatch.Dispatcher
	Identity   agenttls.Identity
	Broker     *runner.LogBroker
	EventBus   *events.Bus
}

type Server struct {
	repository *repository.Repository
	dispatcher *dispatch.Dispatcher
	identity   agenttls.Identity
	broker     *runner.LogBroker
	eventBus   *events.Bus
}

func New(config Config) (*Server, error) {
	if config.Repository == nil || config.Dispatcher == nil {
		return nil, errors.New(
			"agent server requires repository and dispatcher",
		)
	}
	if _, err := agenttls.NewServerConfig(
		config.Identity,
		"",
		agenttls.Fingerprint{},
	); err != nil {
		return nil, err
	}
	return &Server{
		repository: config.Repository,
		dispatcher: config.Dispatcher,
		identity:   config.Identity,
		broker:     config.Broker,
		eventBus:   config.EventBus,
	}, nil
}

func (s *Server) TLSConfig(ctx context.Context) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{s.identity.Certificate},
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyConnection: func(state tls.ConnectionState) error {
			lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			_, err := s.authenticate(lookupCtx, state)
			return err
		},
	}
}

func (s *Server) authenticate(
	ctx context.Context,
	state tls.ConnectionState,
) (db.Agent, error) {
	if state.Version != tls.VersionTLS13 || len(state.PeerCertificates) != 1 {
		return db.Agent{}, errors.New(
			"agent requires TLS 1.3 and one pinned certificate",
		)
	}
	peer := state.PeerCertificates[0]
	pin := agenttls.FingerprintOf(peer.Raw)
	agents, err := s.repository.Queries.ListAgents(ctx)
	if err != nil {
		return db.Agent{}, err
	}
	for _, agent := range agents {
		if agent.Status != "active" || !agent.CertificateFingerprint.Valid ||
			agent.CertificateFingerprint.String != pin.String() {
			continue
		}
		pairing, err := s.repository.Queries.GetAgentPairing(ctx, agent.ID)
		if err != nil || pairing.State != "paired" ||
			pairing.AgentPin != pin.String() || !pairing.ServerPin.Valid ||
			pairing.ServerPin.String != s.identity.Fingerprint.String() {
			return db.Agent{}, errors.New("agent pairing is not committed")
		}
		verify, err := agenttls.NewPairingBootstrapClientConfig(
			"https://agent",
			pin,
		)
		if err != nil {
			return db.Agent{}, err
		}
		if err := verify.VerifyConnection(state); err != nil {
			return db.Agent{}, err
		}
		return agent, nil
	}
	return db.Agent{}, errors.New("agent certificate is not active and pinned")
}

// Authenticated rechecks durable status on every request, including keep-alive.
func (s *Server) Authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		agent, err := s.authenticate(r.Context(), *r.TLS)
		if err != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(
			w,
			withAgentID(r, agentproto.AgentID(agent.ID)),
		)
	})
}

func (s *Server) Maintain(ctx context.Context) error {
	return s.dispatcher.Maintain(ctx)
}

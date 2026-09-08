// Package agentserver owns the dedicated pinned mutual-TLS agent boundary.
package agentserver

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"time"

	"durpdeploy/internal/dispatch"
	"durpdeploy/internal/repository"

	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type Config struct {
	Repository *repository.Repository
	Dispatcher *dispatch.Dispatcher
	Identity   agenttls.Identity
}

type Server struct {
	repository *repository.Repository
	dispatcher *dispatch.Dispatcher
	identity   agenttls.Identity
}

func New(config Config) (*Server, error) {
	if config.Repository == nil || config.Dispatcher == nil {
		return nil, errors.New(
			"agent server requires repository and dispatcher",
		)
	}
	if _, err := agenttls.NewServerConfig(config.Identity, "", agenttls.Fingerprint{}); err != nil {
		return nil, err
	}
	return &Server{config.Repository, config.Dispatcher, config.Identity}, nil
}

func (s *Server) TLSConfig(ctx context.Context) *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{s.identity.Certificate},
		ClientAuth:   tls.RequireAnyClientCert,
		VerifyConnection: func(state tls.ConnectionState) error {
			lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			return s.authenticate(lookupCtx, state)
		},
	}
}

func (s *Server) authenticate(
	ctx context.Context,
	state tls.ConnectionState,
) error {
	if state.Version != tls.VersionTLS13 || len(state.PeerCertificates) != 1 {
		return errors.New("agent requires TLS 1.3 and one pinned certificate")
	}
	peer := state.PeerCertificates[0]
	pin := agenttls.FingerprintOf(peer.Raw)
	agents, err := s.repository.Queries.ListAgents(ctx)
	if err != nil {
		return err
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
			return errors.New("agent pairing is not committed")
		}
		verify, err := agenttls.NewPairingBootstrapClientConfig(
			"https://agent",
			pin,
		)
		if err != nil {
			return err
		}
		return verify.VerifyConnection(state)
	}
	return errors.New("agent certificate is not active and pinned")
}

// Authenticated rechecks durable status on every request, including keep-alive.
func (s *Server) Authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil || s.authenticate(r.Context(), *r.TLS) != nil {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) Maintain(ctx context.Context) error {
	return s.dispatcher.Maintain(ctx, time.Now().Unix())
}

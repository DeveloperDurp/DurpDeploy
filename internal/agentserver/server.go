// Package agentserver serves the dedicated remote-agent TLS endpoint.
package agentserver

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"time"

	"durpdeploy/internal/agenttls"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

// Config supplies the dependencies for the dedicated agent TLS server.
type Config struct {
	Repository       *repository.Repository
	Identity         agenttls.Identity
	Now              func() time.Time
	Broker           *runner.LogBroker
	Events           *events.Bus
	Box              *secret.Box
	PendingServerPin *agenttls.Fingerprint
}

// Server exposes the agent router and its dedicated TLS configuration.
type Server struct {
	repository       *repository.Repository
	identity         agenttls.Identity
	now              func() time.Time
	pollWait         func(context.Context) error
	broker           *runner.LogBroker
	events           *events.Bus
	box              *secret.Box
	pendingServerPin *agenttls.Fingerprint
}

// New constructs a dedicated agent server.
func New(config Config) (*Server, error) {
	if config.Repository == nil || config.Box == nil ||
		len(config.Identity.Certificate.Certificate) != 1 {
		return nil, errors.New(
			"agent server requires repository and TLS identity",
		)
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Server{
		repository:       config.Repository,
		identity:         config.Identity,
		now:              config.Now,
		pollWait:         waitForPoll,
		broker:           config.Broker,
		events:           config.Events,
		box:              config.Box,
		pendingServerPin: config.PendingServerPin,
	}, nil
}

// TLSConfig returns the dedicated TLS 1.3 configuration for the agent listener.
func (server *Server) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{server.identity.Certificate},
		ClientAuth:   tls.RequireAnyClientCert,
	}
}

func parseAgentCertificate(
	raw string,
	now time.Time,
) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(raw))
	if block == nil || block.Type != "CERTIFICATE" ||
		len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("invalid agent certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || now.Before(certificate.NotBefore) ||
		!now.Before(certificate.NotAfter) {
		return nil, errors.New("invalid agent certificate")
	}
	if certificate.SignatureAlgorithm != x509.PureEd25519 {
		return nil, errors.New("invalid agent certificate")
	}
	if _, ok := certificate.PublicKey.(ed25519.PublicKey); !ok {
		return nil, errors.New("invalid agent certificate")
	}
	if err := certificate.CheckSignature(
		certificate.SignatureAlgorithm,
		certificate.RawTBSCertificate,
		certificate.Signature,
	); err != nil {
		return nil, errors.New("invalid agent certificate")
	}
	return certificate, nil
}

func noStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		writer.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(writer, request)
	})
}

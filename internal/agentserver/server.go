// Package agentserver serves the dedicated remote-agent TLS endpoint.
package agentserver

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"
	"time"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
	"durpdeploy/internal/db"
	"durpdeploy/internal/events"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/internal/secret"
)

const defaultEnrollmentConcurrency = 8

// Config supplies the dependencies for the enrollment-only TLS server.
type Config struct {
	Repository               *repository.Repository
	Identity                 agenttls.Identity
	Now                      func() time.Time
	MaxConcurrentEnrollments int
	Broker                   *runner.LogBroker
	Events                   *events.Bus
	Box                      *secret.Box
	PendingServerPin         *agenttls.Fingerprint
}

// Server exposes the enrollment router and its dedicated TLS configuration.
type Server struct {
	repository       *repository.Repository
	identity         agenttls.Identity
	now              func() time.Time
	slots            chan struct{}
	pollWait         func(context.Context) error
	broker           *runner.LogBroker
	events           *events.Bus
	box              *secret.Box
	pendingServerPin *agenttls.Fingerprint
}

// New constructs a server that exposes only the enrollment route.
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
	if config.MaxConcurrentEnrollments == 0 {
		config.MaxConcurrentEnrollments = defaultEnrollmentConcurrency
	}
	if config.MaxConcurrentEnrollments < 1 {
		return nil, errors.New("agent enrollment concurrency must be positive")
	}
	return &Server{
		repository:       config.Repository,
		identity:         config.Identity,
		now:              config.Now,
		slots:            make(chan struct{}, config.MaxConcurrentEnrollments),
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
		ClientAuth:   tls.RequestClientCert,
	}
}

func (server *Server) enroll(
	writer http.ResponseWriter,
	request *http.Request,
) {
	if request.Method != http.MethodPost {
		writer.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	select {
	case server.slots <- struct{}{}:
		defer func() { <-server.slots }()
	default:
		writer.WriteHeader(http.StatusTooManyRequests)
		return
	}

	token, ok := enrollmentToken(request.Header.Get("Authorization"))
	if !ok {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	decoded, err := agentproto.DecodeRequest[agentproto.EnrollmentRequest](
		request.Body,
	)
	if err != nil {
		if errors.Is(err, agentproto.ErrRequestTooLarge) {
			writer.WriteHeader(http.StatusRequestEntityTooLarge)
			return
		}
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	certificate, err := enrolledCertificate(
		decoded.CertificatePEM,
		server.now(),
	)
	if err != nil || !ownsCertificate(request.TLS, certificate) {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err := server.activate(
		request.Context(),
		decoded,
		token,
		certificate,
	); err != nil {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (server *Server) activate(
	ctx context.Context,
	request agentproto.EnrollmentRequest,
	token string,
	certificate *x509.Certificate,
) error {
	now := server.now().Unix()
	hash := sha256.Sum256([]byte(token))
	fingerprint := agenttls.FingerprintOf(certificate.Raw).String()
	return server.repository.WithTx(ctx, func(queries *db.Queries) error {
		consumed, err := queries.ConsumeAgentEnrollmentToken(
			ctx,
			db.ConsumeAgentEnrollmentTokenParams{
				UsedAt:    sql.NullInt64{Int64: now, Valid: true},
				TokenHash: hash[:],
				AgentID:   string(request.AgentID),
				ExpiresAt: now,
			},
		)
		if err != nil || consumed != 1 {
			return errors.New("invalid enrollment authorization")
		}
		_, err = queries.ActivatePendingAgent(
			ctx,
			db.ActivatePendingAgentParams{
				CertificatePem: sql.NullString{
					String: string(request.CertificatePEM), Valid: true,
				},
				CertificateFingerprint: sql.NullString{
					String: fingerprint, Valid: true,
				},
				AgentVersion: sql.NullString{
					String: string(request.AgentVersion), Valid: true,
				},
				LastHeartbeatAt: sql.NullInt64{Int64: now, Valid: true},
				EnrolledAt:      sql.NullInt64{Int64: now, Valid: true},
				UpdatedAt:       now,
				ID:              string(request.AgentID),
			},
		)
		return err
	})
}

func enrollmentToken(header string) (string, bool) {
	parts := strings.SplitN(header, " ", 2)
	if len(parts) != 2 || parts[0] != "Enrollment" || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func enrolledCertificate(
	raw agentproto.CertificatePEM,
	now time.Time,
) (*x509.Certificate, error) {
	block, rest := pem.Decode([]byte(raw))
	if block == nil || block.Type != "CERTIFICATE" ||
		len(strings.TrimSpace(string(rest))) != 0 {
		return nil, errors.New("invalid enrollment certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil || now.Before(certificate.NotBefore) ||
		!now.Before(certificate.NotAfter) {
		return nil, errors.New("invalid enrollment certificate")
	}
	if certificate.SignatureAlgorithm != x509.PureEd25519 {
		return nil, errors.New("invalid enrollment certificate")
	}
	if _, ok := certificate.PublicKey.(ed25519.PublicKey); !ok {
		return nil, errors.New("invalid enrollment certificate")
	}
	if err := certificate.CheckSignature(
		certificate.SignatureAlgorithm,
		certificate.RawTBSCertificate,
		certificate.Signature,
	); err != nil {
		return nil, errors.New("invalid enrollment certificate")
	}
	return certificate, nil
}

func ownsCertificate(
	state *tls.ConnectionState,
	certificate *x509.Certificate,
) bool {
	if state == nil || len(state.PeerCertificates) != 1 {
		return false
	}
	return subtle.ConstantTimeCompare(
		state.PeerCertificates[0].Raw,
		certificate.Raw,
	) == 1
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

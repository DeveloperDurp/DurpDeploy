package agentserver

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/pem"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

type agentServerFixture struct {
	now            time.Time
	repo           *repository.Repository
	listener       *Server
	server         *httptest.Server
	serverIdentity agenttls.Identity
	agentIdentity  agenttls.Identity
	box            *secret.Box
}

func newAgentServerFixture(t *testing.T) *agentServerFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	serverIdentity := loadTestIdentity(t)
	conn, err := migrate.Run(
		"file:" + filepath.Join(t.TempDir(), "agentserver.db") +
			"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new test secret box: %v", err)
	}
	fixture := &agentServerFixture{
		now:            now,
		repo:           repository.New(conn),
		serverIdentity: serverIdentity,
		agentIdentity:  loadTestIdentity(t),
		box:            box,
	}
	listener, err := New(Config{
		Repository: fixture.repo,
		Identity:   serverIdentity,
		Now:        func() time.Time { return fixture.now },
		Box:        box,
	})
	if err != nil {
		t.Fatalf("new agent listener: %v", err)
	}
	server := httptest.NewUnstartedServer(listener.Handler())
	server.TLS = listener.TLSConfig()
	server.StartTLS()
	t.Cleanup(server.Close)
	fixture.listener = listener
	fixture.server = server
	return fixture
}

func loadTestIdentity(t *testing.T) agenttls.Identity {
	t.Helper()
	identity, err := agenttls.LoadOrCreate(t.TempDir(), "https://127.0.0.1")
	if err != nil {
		t.Fatalf("create test identity: %v", err)
	}
	return identity
}

func (fixture *agentServerFixture) createPendingAgent(
	t *testing.T,
	agentID string,
) {
	t.Helper()
	_, err := fixture.repo.Queries.CreatePendingAgent(
		context.Background(),
		db.CreatePendingAgentParams{ID: agentID, Name: agentID},
	)
	if err != nil {
		t.Fatalf("create pending agent: %v", err)
	}
}

func activateFixtureAgent(
	t *testing.T,
	fixture *agentServerFixture,
	agentID string,
	identity agenttls.Identity,
) {
	t.Helper()
	fixture.createPendingAgent(t, agentID)
	now := fixture.now.Unix()
	certificate := certificatePEM(t, identity.Certificate)
	codeHash := sha256.Sum256([]byte(agentID + "-pairing"))
	if _, err := fixture.repo.Queries.CreateAgentPairing(
		context.Background(),
		db.CreateAgentPairingParams{
			AgentID:             agentID,
			PairingCodeHash:     codeHash[:],
			AgentPublicIdentity: certificate,
			AgentPin:            identity.Fingerprint.String(),
			ExpiresAt:           now + 60,
		},
	); err != nil {
		t.Fatalf("create fixture pairing: %v", err)
	}
	if _, err := fixture.repo.Queries.BeginAgentPairing(
		context.Background(),
		db.BeginAgentPairingParams{
			AgentID: agentID, PairingCodeHash: codeHash[:],
			AgentPin: identity.Fingerprint.String(), UpdatedAt: now, Now: now,
		},
	); err != nil {
		t.Fatalf("begin fixture pairing: %v", err)
	}
	if _, err := fixture.repo.Queries.CompleteAgentPairing(
		context.Background(),
		db.CompleteAgentPairingParams{
			AgentPublicIdentity: certificate,
			ServerPublicIdentity: sql.NullString{
				String: certificatePEM(
					t,
					fixture.serverIdentity.Certificate,
				), Valid: true,
			},
			ServerPin: sql.NullString{
				String: fixture.serverIdentity.Fingerprint.String(), Valid: true,
			},
			PairedAt: sql.NullInt64{Int64: now, Valid: true}, UpdatedAt: now,
			AgentID: agentID, PairingCodeHash: codeHash[:],
			AgentPin: identity.Fingerprint.String(),
		},
	); err != nil {
		t.Fatalf("complete fixture pairing: %v", err)
	}
	_, err := fixture.repo.Queries.ActivatePairedAgent(
		context.Background(),
		db.ActivatePairedAgentParams{
			CertificatePem: sql.NullString{String: certificate, Valid: true},
			CertificateFingerprint: sql.NullString{
				String: identity.Fingerprint.String(), Valid: true,
			},
			AgentVersion:    sql.NullString{String: "v1", Valid: true},
			LastHeartbeatAt: sql.NullInt64{Int64: now, Valid: true},
			EnrolledAt:      sql.NullInt64{Int64: now, Valid: true},
			UpdatedAt:       now,
			ID:              agentID,
			PairingCodeHash: codeHash[:],
		},
	)
	if err != nil {
		t.Fatalf("activate paired agent: %v", err)
	}
}

func certificatePEM(t *testing.T, certificate tls.Certificate) string {
	t.Helper()
	if len(certificate.Certificate) != 1 {
		t.Fatal("test identity must have one certificate")
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: certificate.Certificate[0],
	}))
}

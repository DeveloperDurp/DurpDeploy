package agentserver

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/agenttls"
	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

const fixtureToken = "enrollment-token"

type enrollmentFixture struct {
	now            time.Time
	token          string
	repo           *repository.Repository
	listener       *Server
	server         *httptest.Server
	serverIdentity agenttls.Identity
	agentIdentity  agenttls.Identity
	box            *secret.Box
}

func newEnrollmentFixture(t *testing.T) *enrollmentFixture {
	t.Helper()
	now := time.Now().UTC().Truncate(time.Second)
	serverIdentity := loadTestIdentity(t)
	agentIdentity := loadTestIdentity(t)
	conn, err := migrate.Run(
		"file:" + filepath.Join(t.TempDir(), "agentserver.db") +
			"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	repo := repository.New(conn)
	box, err := secret.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatalf("new test secret box: %v", err)
	}
	fixture := &enrollmentFixture{
		now:            now,
		token:          fixtureToken,
		repo:           repo,
		serverIdentity: serverIdentity,
		agentIdentity:  agentIdentity,
		box:            box,
	}
	listener, err := New(Config{
		Repository: repo,
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

func (fixture *enrollmentFixture) createPendingAgent(
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

func (fixture *enrollmentFixture) createToken(
	t *testing.T,
	agentID, token string,
	expiresAt time.Time,
) {
	t.Helper()
	hash := sha256.Sum256([]byte(token))
	err := fixture.repo.Queries.CreateAgentEnrollmentToken(
		context.Background(),
		db.CreateAgentEnrollmentTokenParams{
			TokenHash: hash[:], TokenPrefix: "enroll_", AgentID: agentID,
			ExpiresAt: expiresAt.Unix(),
		},
	)
	if err != nil {
		t.Fatalf("create enrollment token: %v", err)
	}
}

func (fixture *enrollmentFixture) enroll(
	t *testing.T,
	agentID string,
	identity agenttls.Identity,
	token string,
) *http.Response {
	t.Helper()
	return fixture.request(
		t,
		identity,
		token,
		enrollmentJSON(agentID, certificatePEM(t, identity.Certificate)),
	)
}

func (fixture *enrollmentFixture) request(
	t *testing.T,
	identity agenttls.Identity,
	token, body string,
) *http.Response {
	t.Helper()
	clientTLS, err := agenttls.NewClientConfig(
		fixture.server.URL,
		[]agenttls.Fingerprint{fixture.serverIdentity.Fingerprint},
	)
	if err != nil {
		t.Fatalf("new pinned client config: %v", err)
	}
	clientTLS.Certificates = []tls.Certificate{identity.Certificate}
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.server.URL+agentproto.EnrollmentPath,
		bytes.NewBufferString(body),
	)
	if err != nil {
		t.Fatalf("new enrollment request: %v", err)
	}
	request.Header.Set("Authorization", "Enrollment "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := (&http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
	}).Do(request)
	if err != nil {
		t.Fatalf("enrollment request: %v", err)
	}
	t.Cleanup(func() { _ = response.Body.Close() })
	return response
}

func (fixture *enrollmentFixture) enrollStatus() int {
	clientTLS, err := agenttls.NewClientConfig(
		fixture.server.URL,
		[]agenttls.Fingerprint{fixture.serverIdentity.Fingerprint},
	)
	if err != nil {
		return 0
	}
	clientTLS.Certificates = []tls.Certificate{
		fixture.agentIdentity.Certificate,
	}
	request, err := http.NewRequest(
		http.MethodPost,
		fixture.server.URL+agentproto.EnrollmentPath,
		strings.NewReader(enrollmentJSON(
			"agent-a",
			certificatePEMForTest(fixture.agentIdentity.Certificate),
		)),
	)
	if err != nil {
		return 0
	}
	request.Header.Set("Authorization", "Enrollment "+fixture.token)
	response, err := (&http.Client{
		Transport: &http.Transport{TLSClientConfig: clientTLS},
	}).Do(request)
	if err != nil {
		return 0
	}
	defer response.Body.Close()
	return response.StatusCode
}

func enrollmentJSON(agentID, certificate string) string {
	return `{"protocol":"agent/1","agent_id":"` + agentID +
		`","name":"` + agentID +
		`","agent_version":"v1","certificate_pem":` +
		quoteJSON(certificate) + `}`
}

func certificatePEM(t *testing.T, certificate tls.Certificate) string {
	t.Helper()
	if len(certificate.Certificate) != 1 {
		t.Fatal("test identity must have one certificate")
	}
	return certificatePEMForTest(certificate)
}

func certificatePEMForTest(certificate tls.Certificate) string {
	if len(certificate.Certificate) != 1 {
		return ""
	}
	return string(pem.EncodeToMemory(&pem.Block{
		Type: "CERTIFICATE", Bytes: certificate.Certificate[0],
	}))
}

func quoteJSON(value string) string {
	escaped := strings.ReplaceAll(value, `\`, `\\`)
	return `"` + strings.ReplaceAll(escaped, "\n", `\n`) + `"`
}

func assertActiveAgent(
	t *testing.T,
	repo *repository.Repository,
	agentID, fingerprint string,
) {
	t.Helper()
	var status, storedFingerprint string
	err := repo.DB.QueryRowContext(
		context.Background(),
		"SELECT status, certificate_fingerprint FROM agents WHERE id = ?",
		agentID,
	).Scan(&status, &storedFingerprint)
	if err != nil {
		t.Fatalf("read enrolled agent: %v", err)
	}
	if status != "active" || storedFingerprint != fingerprint {
		t.Fatalf(
			"enrolled agent = status %q fingerprint %q",
			status,
			storedFingerprint,
		)
	}
}

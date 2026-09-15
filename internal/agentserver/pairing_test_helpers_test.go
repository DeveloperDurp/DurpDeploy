package agentserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

var errTestInterruption = errors.New("test interruption")

type pairingFixture struct {
	repo       *repository.Repository
	service    *PairingService
	input      PairingInput
	code       string
	box        *secret.Box
	agent      agenttls.Identity
	now        time.Time
	requests   []agentproto.PairRequest
	post       func(agentproto.PairRequest) (int, error)
	connectErr error
}

type fakePairingConnection struct {
	fixture *pairingFixture
	der     []byte
}

func (c *fakePairingConnection) CertificateDER() []byte { return c.der }

func (c *fakePairingConnection) Post(
	_ context.Context,
	request agentproto.PairRequest,
) (int, error) {
	c.fixture.requests = append(c.fixture.requests, request)
	if c.fixture.post != nil {
		return c.fixture.post(request)
	}
	return 204, nil
}

func (c *fakePairingConnection) Close() error { return nil }

func newPairingFixture(t *testing.T) *pairingFixture {
	t.Helper()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	repo := repository.New(conn)
	box, err := secret.NewBox(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	serverIdentity, err := agenttls.LoadOrCreate(
		t.TempDir(),
		"https://server.test",
	)
	if err != nil {
		t.Fatal(err)
	}
	agentIdentity, err := agenttls.LoadOrCreate(
		t.TempDir(),
		"https://agent.test",
	)
	if err != nil {
		t.Fatal(err)
	}
	pullEndpoint, err := agentproto.ParsePullEndpoint(
		"https://server.test:10944",
	)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	service, err := NewPairingService(PairingConfig{
		Repository:   repo,
		Identity:     serverIdentity,
		PullEndpoint: pullEndpoint,
		Secrets:      box,
		Now:          func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	code := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{3}, 32))
	input, err := ParsePairingInput(
		"https://agent.test:10943",
		code,
		agentIdentity.Fingerprint.String(),
	)
	if err != nil {
		t.Fatal(err)
	}
	fixture := &pairingFixture{
		repo: repo, service: service, input: input, box: box,
		agent: agentIdentity, now: now, code: code,
	}
	service.connect = func(
		_ context.Context,
		_ string,
		_ agenttls.Identity,
	) (pairingConnection, error) {
		if fixture.connectErr != nil {
			return nil, fixture.connectErr
		}
		return &fakePairingConnection{
			fixture: fixture,
			der:     fixture.agent.Certificate.Certificate[0],
		}, nil
	}
	return fixture
}

func (f *pairingFixture) pair(t *testing.T) (PairingResult, error) {
	t.Helper()
	return f.service.Pair(context.Background(), f.input)
}

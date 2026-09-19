package agentserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
	agenttls "github.com/DeveloperDurp/durpdeploy-agent/transport"
)

func TestPairingRejectsWrongCode(t *testing.T) {
	fixture := newPairingFixture(t)
	fixture.post = func(_ agentproto.PairRequest) (int, error) { return 401, nil }

	if _, err := fixture.pair(t); !errors.Is(err, ErrPairingConflict) {
		t.Fatalf("err=%v", err)
	}
	var active int
	if err := fixture.repo.DB.QueryRow(
		"SELECT count(*) FROM agents WHERE status = 'active'",
	).Scan(&active); err != nil || active != 0 {
		t.Fatalf("active=%d err=%v", active, err)
	}
}

func TestParsePairingInputRejectsMalformed(t *testing.T) {
	validCode := base64.RawURLEncoding.EncodeToString(
		bytes.Repeat([]byte{3}, 32),
	)
	validPin := strings.Repeat("a", 64)
	tests := []struct {
		name        string
		address     string
		code        string
		fingerprint string
	}{
		{"insecure address", "http://agent.test", validCode, validPin},
		{"address path", "https://agent.test/pair", validCode, validPin},
		{"short code", "https://agent.test", "secret", validPin},
		{"short fingerprint", "https://agent.test", validCode, "pin"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := ParsePairingInput(
				test.address,
				test.code,
				test.fingerprint,
			); err == nil {
				t.Fatal("malformed pairing input accepted")
			}
		})
	}
}

func TestPairingRejectsWrongFingerprint(t *testing.T) {
	fixture := newPairingFixture(t)
	input, err := ParsePairingInput(
		fixture.input.Address(),
		fixture.code,
		strings.Repeat("f", 64),
	)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := fixture.service.Pair(
		context.Background(),
		input,
	); !errors.Is(
		err,
		ErrPairingFingerprint,
	) {
		t.Fatalf("err=%v", err)
	}
	assertNoPairingState(t, fixture)
}

func TestPairingRejectsWrongCertificate(t *testing.T) {
	fixture := newPairingFixture(t)
	fixture.connectErr = errors.New("certificate is not a valid identity")

	if _, err := fixture.pair(t); err == nil {
		t.Fatal("invalid certificate accepted")
	}
	assertNoPairingState(t, fixture)
}

func TestPairingRejectsChangedRecoveryTuple(t *testing.T) {
	tests := []struct {
		name   string
		change func(*testing.T, *pairingFixture)
	}{
		{"code", func(t *testing.T, fixture *pairingFixture) {
			fixture.input = mustPairingInput(
				t,
				fixture,
				fixture.input.Address(),
				base64.RawURLEncoding.EncodeToString(
					bytes.Repeat([]byte{9}, 32),
				),
				fixture.agent.Fingerprint.String(),
			)
		}},
		{"address", func(t *testing.T, fixture *pairingFixture) {
			fixture.input = mustPairingInput(t, fixture,
				"https://other.test:10943", fixture.code,
				fixture.agent.Fingerprint.String())
		}},
		{"agent pin", func(t *testing.T, fixture *pairingFixture) {
			identity, err := agenttls.LoadOrCreate(
				t.TempDir(),
				"https://agent.test",
			)
			if err != nil {
				t.Fatal(err)
			}
			fixture.agent = identity
			fixture.input = mustPairingInput(t, fixture,
				fixture.input.Address(), fixture.code,
				identity.Fingerprint.String())
		}},
		{"server pin", func(t *testing.T, fixture *pairingFixture) {
			identity, err := agenttls.LoadOrCreate(
				t.TempDir(),
				"https://server.test",
			)
			if err != nil {
				t.Fatal(err)
			}
			fixture.service.identity = identity
		}},
		{"pull endpoint", func(t *testing.T, fixture *pairingFixture) {
			endpoint, err := agentproto.ParsePullEndpoint(
				"https://other-server.test",
			)
			if err != nil {
				t.Fatal(err)
			}
			fixture.service.pullEndpoint = endpoint
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newPairingFixture(t)
			fixture.service.afterPersist = func() error { return errTestInterruption }
			if _, err := fixture.pair(t); err == nil {
				t.Fatal("interruption reported success")
			}
			fixture.service.afterPersist = nil
			test.change(t, fixture)
			if _, err := fixture.pair(t); !errors.Is(err, ErrPairingConflict) {
				t.Fatalf("err=%v", err)
			}
			if len(fixture.requests) != 0 {
				t.Fatal("changed tuple reached HTTP")
			}
		})
	}
}

func TestPairingUncommittedTupleExpires(t *testing.T) {
	fixture := newPairingFixture(t)
	fixture.service.afterPersist = func() error { return errTestInterruption }
	if _, err := fixture.pair(t); err == nil {
		t.Fatal("interruption reported success")
	}
	fixture.service.afterPersist = nil
	fixture.now = fixture.now.Add(11 * time.Minute)
	fixture.service.now = func() time.Time { return fixture.now }
	fixture.post = func(_ agentproto.PairRequest) (int, error) { return 401, nil }

	if _, err := fixture.pair(t); !errors.Is(err, ErrPairingConflict) {
		t.Fatalf("err=%v", err)
	}
	var state string
	if err := fixture.repo.DB.QueryRow(
		"SELECT state FROM agent_pairings",
	).Scan(&state); err != nil || state != "expired" {
		t.Fatalf("state=%q err=%v", state, err)
	}
	freshCode := base64.RawURLEncoding.EncodeToString(
		bytes.Repeat([]byte{8}, 32),
	)
	fixture.input = mustPairingInput(
		t, fixture, fixture.input.Address(), freshCode,
		fixture.agent.Fingerprint.String(),
	)
	fixture.post = nil
	if result, err := fixture.pair(
		t,
	); err != nil ||
		result.State != PairingStatePaired {
		t.Fatalf("fresh pairing result=%+v err=%v", result, err)
	}
}

func assertNoPairingState(t *testing.T, fixture *pairingFixture) {
	t.Helper()
	var count int
	if err := fixture.repo.DB.QueryRow("SELECT count(*) FROM agents").
		Scan(&count); err != nil ||
		count != 0 {
		t.Fatalf("agents=%d err=%v", count, err)
	}
	if len(fixture.requests) != 0 {
		t.Fatalf("HTTP requests=%d", len(fixture.requests))
	}
}

func mustPairingInput(
	t *testing.T,
	fixture *pairingFixture,
	address string,
	code string,
	fingerprint string,
) PairingInput {
	t.Helper()
	input, err := ParsePairingInput(address, code, fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	return input
}

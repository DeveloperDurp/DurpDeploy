package mfa

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestChallenge_ConsumeInvokesOneCallbackWhenConcurrent(t *testing.T) {
	// Given: a single bound challenge and concurrent consumers.
	ctx := context.Background()
	service, binding, _ := newChallengeServiceForTest(t)
	start := make(chan struct{})
	var callbacks int
	var callbacksMu sync.Mutex
	var group sync.WaitGroup

	// When: both consumers submit the same correct binding.
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			_ = service.Consume(
				ctx,
				binding,
				func(context.Context, db.MfaChallenge) error {
					callbacksMu.Lock()
					callbacks++
					callbacksMu.Unlock()
					return nil
				},
			)
		}()
	}
	close(start)
	group.Wait()

	// Then: exactly one caller reaches elevation.
	callbacksMu.Lock()
	deferredCallbacks := callbacks
	callbacksMu.Unlock()
	if deferredCallbacks != 1 {
		t.Fatalf("callbacks = %d, want 1", deferredCallbacks)
	}
}

func TestChallenge_ConsumeRejectsWrongBindingsAndExpiry(t *testing.T) {
	// Given: independently issued challenges for each invalid binding.
	ctx := context.Background()
	tests := []struct {
		name   string
		mutate func(*ChallengeBinding, *ChallengeService)
	}{
		{
			name:   "wrong user",
			mutate: func(binding *ChallengeBinding, _ *ChallengeService) { binding.UserID++ },
		},
		{
			name:   "wrong purpose",
			mutate: func(binding *ChallengeBinding, _ *ChallengeService) { binding.Purpose = ChallengePurposeRecoveryVerify },
		},
		{
			name:   "wrong session",
			mutate: func(binding *ChallengeBinding, _ *ChallengeService) { binding.SessionID = "other" },
		},
		{
			name:   "wrong csrf",
			mutate: func(binding *ChallengeBinding, _ *ChallengeService) { binding.CSRF = "wrong" },
		},
		{
			name: "expired",
			mutate: func(_ *ChallengeBinding, service *ChallengeService) {
				service.clock = func() time.Time { return time.Unix(1_700_000_301, 0) }
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service, binding, _ := newChallengeServiceForTest(t)
			test.mutate(&binding, service)
			called := false

			// When: the invalid binding is consumed.
			err := service.Consume(
				ctx,
				binding,
				func(context.Context, db.MfaChallenge) error {
					called = true
					return nil
				},
			)

			// Then: no elevation callback runs.
			if !errors.Is(err, ErrChallengeInvalid) || called {
				t.Fatalf("consume error = %v, callback = %t", err, called)
			}
		})
	}
}

func TestChallenge_IssuePersistsOnlyHashes(t *testing.T) {
	// Given: a deterministic pending challenge issuer.
	ctx := context.Background()
	_, binding, conn := newChallengeServiceForTest(t)

	// When: the opaque token and separate CSRF value are issued.

	// Then: neither raw value is present in the database row.
	var tokenHash, csrfHash []byte
	if err := conn.QueryRowContext(
		ctx,
		"SELECT token_hash, csrf_hash FROM mfa_challenges WHERE user_id = ?",
		binding.UserID,
	).Scan(&tokenHash, &csrfHash); err != nil {
		t.Fatalf("load challenge hashes: %v", err)
	}
	if bytes.Equal(tokenHash, []byte(binding.Token)) ||
		bytes.Equal(csrfHash, []byte(binding.CSRF)) {
		t.Fatal("raw pending challenge value persisted")
	}
	if len(tokenHash) != 32 || len(csrfHash) != 32 {
		t.Fatal("challenge hashes were not persisted")
	}
}

func TestChallenge_IssueAcceptsPendingLoginPurpose(t *testing.T) {
	// Given
	service, binding, _ := newChallengeServiceForTest(t)

	// When
	_, err := service.Issue(context.Background(), ChallengeIssue{
		UserID: binding.UserID, Purpose: ChallengePurposeLoginMFA,
	})

	// Then
	if err != nil {
		t.Fatalf("issue pending login challenge: %v", err)
	}
}

func TestChallenge_IssueStoresCeremonyDataServerSide(t *testing.T) {
	// Given: a server-side ceremony payload at issue time.
	ctx := context.Background()
	service, binding, conn := newChallengeServiceForTest(t)
	session := webauthn.SessionData{Challenge: "server-side"}
	ceremonyJSON, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal WebAuthn session data: %v", err)
	}

	// When: a WebAuthn assertion challenge is issued.
	if _, err := service.IssueWebAuthn(ctx, ChallengeIssue{
		UserID: binding.UserID, SessionID: binding.SessionID,
		Purpose: ChallengePurposeWebAuthnAuth,
	}, session); err != nil {
		t.Fatalf("issue WebAuthn challenge: %v", err)
	}

	// Then: the ceremony payload is retrievable only from the challenge row.
	var stored string
	if err := conn.QueryRowContext(
		ctx,
		"SELECT ceremony_json FROM mfa_challenges WHERE purpose = ?",
		"webauthn_auth",
	).Scan(&stored); err != nil {
		t.Fatalf("load ceremony data: %v", err)
	}
	if stored != string(ceremonyJSON) {
		t.Fatal("WebAuthn ceremony data was not stored server-side")
	}
}

func TestChallenge_ConsumeCleansExpiredRows(t *testing.T) {
	// Given: a challenge that expires before it is consumed.
	ctx := context.Background()
	service, binding, conn := newChallengeServiceForTest(t)
	service.clock = func() time.Time { return time.Unix(1_700_000_301, 0) }

	// When: the expired challenge is submitted.
	err := service.Consume(
		ctx,
		binding,
		func(context.Context, db.MfaChallenge) error {
			t.Fatal("expired challenge elevated")
			return nil
		},
	)

	// Then: it is rejected and removed during use-time cleanup.
	if !errors.Is(err, ErrChallengeInvalid) {
		t.Fatalf(
			"expired consume error = %v, want %v",
			err,
			ErrChallengeInvalid,
		)
	}
	var remaining int
	if err := conn.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM mfa_challenges WHERE user_id = ?", binding.UserID,
	).Scan(&remaining); err != nil {
		t.Fatalf("count expired challenges: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("expired challenges after consume = %d, want 0", remaining)
	}
}

func newChallengeServiceForTest(
	t *testing.T,
) (*ChallengeService, ChallengeBinding, *sql.DB) {
	t.Helper()
	conn, err := migrate.Run(
		"file:" + t.TempDir() + "/mfa.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	conn.SetMaxOpenConns(1)
	repo := repository.New(conn)
	ctx := context.Background()
	user, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email: "service@example.test", PasswordHash: "hash", Name: "Service", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if _, err := repo.Queries.CreateSession(ctx, db.CreateSessionParams{
		ID: "session", UserID: user.ID, CsrfToken: "csrf", ExpiresAt: 1_700_003_600,
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}
	random := make([]byte, 512)
	for i := range random {
		random[i] = byte(i/32 + 1)
	}
	service := NewChallengeService(ChallengeServiceConfig{
		Repository: repo,
		Clock:      func() time.Time { return time.Unix(1_700_000_000, 0) },
		Random:     bytes.NewReader(random),
	})
	pending, err := service.Issue(ctx, ChallengeIssue{
		UserID: user.ID, SessionID: "session", Purpose: ChallengePurposeTOTPVerify,
	})
	if err != nil {
		t.Fatalf("issue challenge: %v", err)
	}
	return service, ChallengeBinding{
		Token: pending.Token, CSRF: pending.CSRF, UserID: user.ID, SessionID: "session",
		Purpose: ChallengePurposeTOTPVerify,
	}, conn
}

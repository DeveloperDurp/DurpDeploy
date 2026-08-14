package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
)

func TestAuthDB_BaselineAuthArtifacts(t *testing.T) {
	// Given a migrated database.
	ctx := context.Background()
	queries, _ := newAuthTestDB(t)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "baseline@example.com", PasswordHash: "hash", Name: "Baseline", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// When the existing user, session, and API token contracts are used.
	_, err = queries.CreateSession(ctx, db.CreateSessionParams{
		ID: "baseline-session", UserID: user.ID, CsrfToken: "csrf", ExpiresAt: 1_700_003_600,
		IpAddress: sql.NullString{}, UserAgent: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	_, err = queries.CreateApiToken(ctx, db.CreateApiTokenParams{
		ID: "baseline-token", UserID: user.ID, Name: "Baseline", TokenPrefix: "ddp_pat_",
		TokenHash: "baseline-token-hash", Scope: "global", ExpiresAt: sql.NullInt64{},
	})
	if err != nil {
		t.Fatalf("create API token: %v", err)
	}

	// Then their baseline persistence contracts remain available.
	if _, err := queries.GetSession(ctx, db.GetSessionParams{
		ID: "baseline-session", ExpiresAt: 1_700_000_000,
	}); err != nil {
		t.Fatalf("get session: %v", err)
	}
}

func TestMFASchema_RejectsInvalidAndDuplicateArtifacts(t *testing.T) {
	// Given a migrated database and an MFA user.
	ctx := context.Background()
	_, conn := newAuthTestDB(t)
	_, err := conn.ExecContext(ctx, `
		INSERT INTO users (email, password_hash, name, role)
		VALUES ('mfa@example.com', 'hash', 'MFA', 'admin')`)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	// When each MFA table is inspected and representative invalid writes occur.
	for _, table := range []string{
		"mfa_totp", "webauthn_users", "webauthn_credentials",
		"mfa_recovery_codes", "mfa_challenges", "mfa_rate_limits",
	} {
		var name string
		err := conn.QueryRowContext(
			ctx,
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?",
			table,
		).Scan(&name)
		if err != nil {
			t.Fatalf("find %s: %v", table, err)
		}
	}
	_, err = conn.ExecContext(ctx, `
		INSERT INTO mfa_recovery_codes (id, user_id, code_hash)
		VALUES (NULL, 1, X'0404040404040404040404040404040404040404040404040404040404040404')`)
	if err == nil {
		t.Fatal("NULL recovery-code ID succeeded")
	}
	_, err = conn.ExecContext(ctx, `
		INSERT INTO mfa_challenges
		(token_hash, user_id, purpose, csrf_hash, ceremony_json, expires_at)
		VALUES (X'0000000000000000000000000000000000000000000000000000000000000000', 1,
			'invalid', X'0000000000000000000000000000000000000000000000000000000000000000',
			'{}', 1700003600)`)
	if err == nil {
		t.Fatal("invalid challenge purpose succeeded")
	}

	// Then duplicate credential, recovery hash, and challenge hash writes fail.
	statements := []string{
		`INSERT INTO webauthn_users (user_id, rp_id, user_handle) VALUES (1, 'example.test', X'0000000000000000000000000000000000000000000000000000000000000000')`,
		`INSERT INTO webauthn_credentials (credential_id, user_id, name, public_key, transports_json, flags, sign_count) VALUES (X'01', 1, 'key', X'01', '[]', 0, 0)`,
		`INSERT INTO webauthn_credentials (credential_id, user_id, name, public_key, transports_json, flags, sign_count) VALUES (X'01', 1, 'other', X'01', '[]', 0, 0)`,
		`INSERT INTO mfa_recovery_codes (id, user_id, code_hash) VALUES ('code-1', 1, X'0101010101010101010101010101010101010101010101010101010101010101')`,
		`INSERT INTO mfa_recovery_codes (id, user_id, code_hash) VALUES ('code-2', 1, X'0101010101010101010101010101010101010101010101010101010101010101')`,
		`INSERT INTO mfa_challenges (token_hash, user_id, purpose, csrf_hash, ceremony_json, expires_at) VALUES (X'0202020202020202020202020202020202020202020202020202020202020202', 1, 'totp_verify', X'0303030303030303030303030303030303030303030303030303030303030303', '{}', 1700003600)`,
		`INSERT INTO mfa_challenges (token_hash, user_id, purpose, csrf_hash, ceremony_json, expires_at) VALUES (X'0202020202020202020202020202020202020202020202020202020202020202', 1, 'totp_verify', X'0303030303030303030303030303030303030303030303030303030303030303', '{}', 1700003600)`,
	}
	for i, statement := range statements {
		_, err := conn.ExecContext(ctx, statement)
		if i == 2 || i == 4 || i == 6 {
			if err == nil {
				t.Fatalf("duplicate MFA artifact %d succeeded", i)
			}
			continue
		}
		if err != nil {
			t.Fatalf("create MFA artifact %d: %v", i, err)
		}
	}
}

func TestMFASchema_ChallengeConsumptionAllowsOneWinner(t *testing.T) {
	// Given a single pending challenge and concurrent access to one database.
	ctx := context.Background()
	queries, conn := newAuthTestDB(t)
	conn.SetMaxOpenConns(1)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "challenge@example.com", PasswordHash: "hash", Name: "Challenge", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	tokenHash := make([]byte, 32)
	if _, err := queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: tokenHash, UserID: user.ID, Purpose: "totp_verify",
		CsrfHash: make(
			[]byte,
			32,
		), CeremonyJson: "{}", ExpiresAt: 1_700_003_600,
	}); err != nil {
		t.Fatalf("create challenge: %v", err)
	}

	// When two consumers start together.
	type result struct {
		rows int64
		err  error
	}
	start := make(chan struct{})
	results := make(chan result, 2)
	for range 2 {
		go func() {
			<-start
			rows, err := queries.ConsumeMFAChallenge(ctx, tokenHash)
			results <- result{rows: rows, err: err}
		}()
	}
	close(start)

	// Then exactly one consumer observes ownership of the challenge.
	var consumed int64
	for range 2 {
		got := <-results
		if got.err != nil {
			t.Fatalf("consume challenge: %v", got.err)
		}
		consumed += got.rows
	}
	if consumed != 1 {
		t.Fatalf("consumed rows = %d, want 1", consumed)
	}
}

func TestMFASchema_QueryContracts(t *testing.T) {
	// Given a user with a live session in a migrated database.
	ctx := context.Background()
	queries, _ := newAuthTestDB(t)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "contracts@example.com", PasswordHash: "hash", Name: "Contracts", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	_, err = queries.CreateSession(ctx, db.CreateSessionParams{
		ID: "contracts-session", UserID: user.ID, CsrfToken: "csrf", ExpiresAt: 1_700_003_600,
		IpAddress: sql.NullString{}, UserAgent: sql.NullString{},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	// When MFA factors, recovery, challenges, throttling, and reauthentication are persisted.
	if _, err := queries.CreateTOTP(ctx, db.CreateTOTPParams{
		UserID: user.ID, EncryptedSeed: []byte("encrypted-seed"), LastAcceptedStep: sql.NullInt64{},
	}); err != nil {
		t.Fatalf("create TOTP: %v", err)
	}
	if _, err := queries.CreateWebAuthnUser(ctx, db.CreateWebAuthnUserParams{
		UserID: user.ID, RpID: "example.test", UserHandle: make([]byte, 32),
	}); err != nil {
		t.Fatalf("create WebAuthn user: %v", err)
	}
	credentialID := []byte{1}
	if _, err := queries.CreateWebAuthnCredential(
		ctx,
		db.CreateWebAuthnCredentialParams{
			CredentialID: credentialID, UserID: user.ID, Name: "primary", PublicKey: []byte{2},
			Aaguid: nil, TransportsJson: "[]", Flags: 0, SignCount: 0, CloneWarning: 0,
			Attachment: sql.NullString{},
		},
	); err != nil {
		t.Fatalf("create WebAuthn credential: %v", err)
	}
	if _, err := queries.UpdateWebAuthnCredentialCounter(ctx,
		db.UpdateWebAuthnCredentialCounterParams{
			CredentialID: credentialID, SignCount: 1, CloneWarning: 0,
		}); err != nil {
		t.Fatalf("update WebAuthn credential counter: %v", err)
	}
	recoveryHash := make([]byte, 32)
	recoveryHash[0] = 1
	if _, err := queries.CreateRecoveryCode(ctx, db.CreateRecoveryCodeParams{
		ID: "recovery-1", UserID: user.ID, CodeHash: recoveryHash,
	}); err != nil {
		t.Fatalf("create recovery code: %v", err)
	}
	if _, err := queries.ConsumeRecoveryCode(ctx, db.ConsumeRecoveryCodeParams{
		UsedAt: sql.NullInt64{
			Int64: 1_700_000_000,
			Valid: true,
		}, UserID: user.ID, CodeHash: recoveryHash,
	}); err != nil {
		t.Fatalf("consume recovery code: %v", err)
	}
	challengeHash := make([]byte, 32)
	challengeHash[0] = 2
	if _, err := queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: challengeHash, UserID: user.ID, SessionID: sql.NullString{String: "contracts-session", Valid: true},
		Purpose: "totp_verify", CsrfHash: make([]byte, 32), CeremonyJson: "{}", Attempts: 0, ExpiresAt: 1_700_003_600,
	}); err != nil {
		t.Fatalf("create MFA challenge: %v", err)
	}
	if _, err := queries.IncrementMFAChallengeAttempts(
		ctx,
		db.IncrementMFAChallengeAttemptsParams{
			TokenHash: challengeHash,
			Attempts:  3,
		},
	); err != nil {
		t.Fatalf("increment MFA challenge attempts: %v", err)
	}
	if _, err := queries.CreateMFARateLimit(ctx, db.CreateMFARateLimitParams{
		UserID: user.ID, WindowStartedAt: 1_700_000_000, FailureCount: 1, BlockedUntil: sql.NullInt64{},
	}); err != nil {
		t.Fatalf("create MFA rate limit: %v", err)
	}
	if err := queries.MarkSessionReauthenticated(
		ctx,
		db.MarkSessionReauthenticatedParams{
			ID: "contracts-session", ReauthenticatedAt: sql.NullInt64{Int64: 1_700_000_001, Valid: true},
		},
	); err != nil {
		t.Fatalf("mark session reauthenticated: %v", err)
	}

	// Then consumed recovery codes cannot be reused and the session keeps its MFA timestamp.
	if _, err := queries.ConsumeRecoveryCode(ctx, db.ConsumeRecoveryCodeParams{
		UsedAt: sql.NullInt64{
			Int64: 1_700_000_002,
			Valid: true,
		}, UserID: user.ID, CodeHash: recoveryHash,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("reuse recovery code error = %v, want sql.ErrNoRows", err)
	}
	session, err := queries.GetSession(ctx, db.GetSessionParams{
		ID: "contracts-session", ExpiresAt: 1_700_000_000,
	})
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if !session.ReauthenticatedAt.Valid ||
		session.ReauthenticatedAt.Int64 != 1_700_000_001 {
		t.Fatalf("reauthenticated_at = %#v", session.ReauthenticatedAt)
	}
}

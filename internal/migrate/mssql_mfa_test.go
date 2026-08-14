package migrate

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/pressly/goose/v3"

	"durpdeploy/internal/db"
)

func TestMSSQL_MFASchemaParity(t *testing.T) {
	// Given a native SQL Server database migrated from the embedded migrations.
	ctx := context.Background()
	conn := newSQLServerTestDB(t)
	queries := db.New(conn)
	user, err := queries.CreateUser(ctx, db.CreateUserParams{
		Email: "mssql-mfa@example.com", PasswordHash: "hash", Name: "MFA", Role: "admin",
	})
	requireNoError(t, err, "create user")
	locked, err := queries.LockMFAUser(ctx, user.ID)
	requireNoError(t, err, "lock MFA user")
	if locked != 1 {
		t.Fatalf("locked MFA users = %d, want 1", locked)
	}
	_, err = queries.CreateSession(ctx, db.CreateSessionParams{
		ID: "mssql-mfa-session", UserID: user.ID, CsrfToken: "csrf", ExpiresAt: 1_700_003_600,
		IpAddress: sql.NullString{}, UserAgent: sql.NullString{},
	})
	requireNoError(t, err, "create session")

	// When every MFA table and representative binary contract is exercised.
	for _, table := range []string{
		"mfa_totp", "webauthn_users", "webauthn_credentials",
		"mfa_recovery_codes", "mfa_challenges", "mfa_rate_limits",
	} {
		var count int
		err := conn.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sys.tables WHERE name = @p1", table,
		).Scan(&count)
		requireNoError(t, err, "find "+table)
		if count != 1 {
			t.Fatalf("table %s count = %d, want 1", table, count)
		}
	}
	_, err = queries.CreateTOTP(ctx, db.CreateTOTPParams{
		UserID: user.ID, EncryptedSeed: []byte("encrypted-seed"), LastAcceptedStep: sql.NullInt64{},
	})
	requireNoError(t, err, "create TOTP")
	totp, err := queries.UpdateTOTP(ctx, db.UpdateTOTPParams{
		UserID: user.ID, EncryptedSeed: []byte("updated-encrypted-seed"),
		LastAcceptedStep: sql.NullInt64{Int64: 42, Valid: true},
	})
	requireNoError(t, err, "update TOTP")
	if !totp.LastAcceptedStep.Valid || totp.LastAcceptedStep.Int64 != 42 {
		t.Fatalf("updated TOTP step = %#v", totp.LastAcceptedStep)
	}
	_, err = queries.CreateWebAuthnUser(ctx, db.CreateWebAuthnUserParams{
		UserID: user.ID, RpID: "example.test", UserHandle: make([]byte, 32),
	})
	requireNoError(t, err, "create WebAuthn user")
	credentialID := []byte{1}
	credential := db.CreateWebAuthnCredentialParams{
		CredentialID: credentialID, UserID: user.ID, Name: "primary", PublicKey: []byte{2},
		Aaguid: nil, TransportsJson: "[]", Flags: 0, SignCount: 0, CloneWarning: 0,
		Attachment: sql.NullString{},
	}
	_, err = queries.CreateWebAuthnCredential(ctx, credential)
	requireNoError(t, err, "create WebAuthn credential")
	updatedCredential, err := queries.UpdateWebAuthnCredentialCounter(
		ctx,
		db.UpdateWebAuthnCredentialCounterParams{
			CredentialID: credentialID, SignCount: 7, CloneWarning: 1,
		},
	)
	requireNoError(t, err, "update WebAuthn credential")
	if updatedCredential.SignCount != 7 || updatedCredential.CloneWarning != 1 {
		t.Fatalf("updated WebAuthn credential = %#v", updatedCredential)
	}
	_, err = queries.CreateWebAuthnCredential(ctx, credential)
	if err == nil {
		t.Fatal("duplicate WebAuthn credential succeeded")
	}
	recoveryHash := make([]byte, 32)
	recoveryHash[0] = 1
	_, err = queries.CreateRecoveryCode(ctx, db.CreateRecoveryCodeParams{
		ID: "mssql-recovery", UserID: user.ID, CodeHash: recoveryHash,
	})
	requireNoError(t, err, "create recovery code")
	_, err = queries.CreateRecoveryCode(ctx, db.CreateRecoveryCodeParams{
		ID: "mssql-recovery-duplicate", UserID: user.ID, CodeHash: recoveryHash,
	})
	if err == nil {
		t.Fatal("duplicate recovery hash succeeded")
	}
	_, err = queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: make(
			[]byte,
			32,
		), UserID: user.ID, SessionID: sql.NullString{String: "mssql-mfa-session", Valid: true},
		Purpose: "totp_verify", CsrfHash: recoveryHash, CeremonyJson: "{}", Attempts: 0, ExpiresAt: 1_700_003_600,
	})
	requireNoError(t, err, "create MFA challenge")
	invalidPurposeHash := make([]byte, 32)
	invalidPurposeHash[0] = 3
	_, err = queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: invalidPurposeHash, UserID: user.ID, Purpose: "invalid",
		CsrfHash: recoveryHash, CeremonyJson: "{}", Attempts: 0,
		ExpiresAt: 1_700_003_600,
	})
	if err == nil {
		t.Fatal("invalid MFA challenge purpose succeeded")
	}
	consumeHash := make([]byte, 32)
	consumeHash[0] = 1
	_, err = queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: consumeHash, UserID: user.ID, Purpose: "login_mfa",
		CsrfHash: recoveryHash, CeremonyJson: "{}", Attempts: 0,
		ExpiresAt: 1_700_003_600,
	})
	requireNoError(t, err, "create consumable MFA challenge")
	consumed, err := queries.ConsumeMFAChallenge(ctx, consumeHash)
	requireNoError(t, err, "consume MFA challenge")
	if consumed != 1 {
		t.Fatalf("consumed MFA challenges = %d, want 1", consumed)
	}
	consumed, err = queries.ConsumeMFAChallenge(ctx, consumeHash)
	requireNoError(t, err, "consume MFA challenge again")
	if consumed != 0 {
		t.Fatalf("reconsumed MFA challenges = %d, want 0", consumed)
	}
	_, err = queries.CreateMFARateLimit(ctx, db.CreateMFARateLimitParams{
		UserID: user.ID, WindowStartedAt: 1_700_000_000, FailureCount: 1, BlockedUntil: sql.NullInt64{},
	})
	requireNoError(t, err, "create MFA rate limit")
	err = queries.MarkSessionReauthenticated(
		ctx,
		db.MarkSessionReauthenticatedParams{
			ID: "mssql-mfa-session", ReauthenticatedAt: sql.NullInt64{Int64: 1_700_000_001, Valid: true},
		},
	)
	requireNoError(t, err, "mark session reauthenticated")

	// Then the session query returns the timestamp assigned by the MFA contract.
	session, err := queries.GetSession(ctx, db.GetSessionParams{
		ID: "mssql-mfa-session", ExpiresAt: 1_700_000_000,
	})
	requireNoError(t, err, "get reauthenticated session")
	if !session.ReauthenticatedAt.Valid ||
		session.ReauthenticatedAt.Int64 != 1_700_000_001 {
		t.Fatalf("reauthenticated_at = %#v", session.ReauthenticatedAt)
	}

	// When SQL Server's intentional NO ACTION session FK blocks direct deletion.
	err = queries.DeleteSession(ctx, "mssql-mfa-session")
	if err == nil {
		t.Fatal("session deletion with a bound challenge succeeded")
	}
	deleted, err := queries.DeleteMFAChallengesBySessionID(ctx,
		sql.NullString{String: "mssql-mfa-session", Valid: true})
	requireNoError(t, err, "delete challenges by session")
	if deleted != 1 {
		t.Fatalf("deleted session challenges = %d, want 1", deleted)
	}
	requireNoError(t, queries.DeleteSession(ctx, "mssql-mfa-session"),
		"delete session after challenge cleanup")

	// Then user-scoped cleanup supports later transactional invalidation.
	_, err = queries.CreateSession(ctx, db.CreateSessionParams{
		ID: "mssql-user-cleanup-session", UserID: user.ID, CsrfToken: "csrf-2",
		ExpiresAt: 1_700_003_600, IpAddress: sql.NullString{}, UserAgent: sql.NullString{},
	})
	requireNoError(t, err, "create cleanup session")
	cleanupHash := make([]byte, 32)
	cleanupHash[0] = 2
	_, err = queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: cleanupHash, UserID: user.ID,
		SessionID: sql.NullString{
			String: "mssql-user-cleanup-session",
			Valid:  true,
		},
		Purpose: "totp_verify", CsrfHash: recoveryHash, CeremonyJson: "{}",
		Attempts: 0, ExpiresAt: 1_700_003_600,
	})
	requireNoError(t, err, "create user cleanup challenge")
	deleted, err = queries.DeleteMFAChallengesByUserID(ctx, user.ID)
	requireNoError(t, err, "delete challenges by user")
	if deleted != 1 {
		t.Fatalf("deleted user challenges = %d, want 1", deleted)
	}
	requireNoError(t, queries.DeleteSessionsByUser(ctx, user.ID),
		"delete sessions after user challenge cleanup")

	requireNoError(t, queries.DeleteWebAuthnCredential(ctx, credentialID),
		"delete WebAuthn credential")
	if _, err := queries.GetWebAuthnCredentialByID(
		ctx,
		credentialID,
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("get deleted credential error = %v, want sql.ErrNoRows", err)
	}
	deleted, err = queries.DeleteTOTP(ctx, user.ID)
	requireNoError(t, err, "delete TOTP")
	if deleted != 1 {
		t.Fatalf("deleted TOTP rows = %d, want 1", deleted)
	}
	if _, err := queries.GetTOTPByUserID(
		ctx,
		user.ID,
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf("get deleted TOTP error = %v, want sql.ErrNoRows", err)
	}

	resetHash := make([]byte, 32)
	resetHash[0] = 4
	_, err = queries.CreateMFAChallenge(ctx, db.CreateMFAChallengeParams{
		TokenHash: resetHash, UserID: user.ID, Purpose: "admin_mfa_reset",
		CsrfHash: recoveryHash, CeremonyJson: "{}", Attempts: 0,
		ExpiresAt: 1_700_003_600,
	})
	requireNoError(t, err, "create admin MFA reset challenge")
	requireNoError(t, goose.DownTo(conn, ".", 7),
		"roll back admin MFA reset migration")
	requireNoError(t, goose.Up(conn, "."), "reapply admin MFA reset migration")

	// Finally the native migration reverses and reapplies cleanly.
	requireNoError(t, goose.DownTo(conn, ".", 4), "roll back MFA migration")
	var tableCount int
	err = conn.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM sys.tables WHERE name IN (
			'mfa_totp', 'webauthn_users', 'webauthn_credentials',
			'mfa_recovery_codes', 'mfa_challenges', 'mfa_rate_limits'
		)`).Scan(&tableCount)
	requireNoError(t, err, "count MFA tables after rollback")
	if tableCount != 0 {
		t.Fatalf("MFA tables after rollback = %d, want 0", tableCount)
	}
	requireNoError(t, goose.Up(conn, "."), "reapply MFA migration")
}

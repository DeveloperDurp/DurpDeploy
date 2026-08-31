package migrate

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	generated "durpdeploy/internal/db"
)

// TestPostgres_MigrationsRun verifies migrations apply cleanly against a
// real PostgreSQL instance, spun up on demand via testcontainers-go. It is
// skipped automatically (via testcontainers-go's own Docker/Podman
// detection) when no container runtime is available, e.g. in CI.
func TestPostgres_MigrationsRun(t *testing.T) {
	ctx := context.Background()
	ctr, err := postgres.Run(ctx, "postgres:16-alpine",
		postgres.WithDatabase("durpdeploy"),
		postgres.WithUsername("durpdeploy"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("5432/tcp")),
	)
	if err != nil {
		t.Skipf("could not start postgres container: %v", err)
	}
	t.Cleanup(func() {
		if err := ctr.Terminate(context.Background()); err != nil {
			t.Logf("terminate container: %v", err)
		}
	})

	dsn, err := ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	db, err := Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	defer db.Close()

	rows, err := db.Query(
		"SELECT table_name FROM information_schema.tables WHERE table_schema='public' ORDER BY table_name",
	)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		t.Log(name)
	}

	// Exercise a real query using ? placeholders + unixepoch() default,
	// mirroring what sqlc-generated code does.
	var id int64
	err = db.QueryRow(`INSERT INTO projects (name, description) VALUES (?, ?) RETURNING id`, "demo", "desc").
		Scan(&id)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	t.Logf("inserted project id=%d", id)

	var cnt int64
	if err := db.QueryRow(`SELECT COUNT(*) FROM deployments WHERE created_at >= strftime('%s','now','start of day')`).
		Scan(&cnt); err != nil {
		t.Fatalf("strftime query: %v", err)
	}
	t.Logf("today count=%d", cnt)

	// Given the PostgreSQL migration has created every MFA table.
	for _, table := range []string{
		"mfa_totp", "webauthn_users", "webauthn_credentials",
		"mfa_recovery_codes", "mfa_challenges", "mfa_rate_limits",
	} {
		var count int
		err := db.QueryRow(
			"SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = 'public' AND table_name = ?",
			table,
		).Scan(&count)
		if err != nil {
			t.Fatalf("find %s: %v", table, err)
		}
		if count != 1 {
			t.Fatalf("table %s count = %d, want 1", table, count)
		}
	}
	var userID int64
	err = db.QueryRow(
		"INSERT INTO users (email, password_hash, name, role) VALUES (?, ?, ?, ?) RETURNING id",
		"postgres-mfa@example.com",
		"hash",
		"Postgres MFA",
		"admin",
	).Scan(&userID)
	if err != nil {
		t.Fatalf("create MFA user: %v", err)
	}
	locked, err := generated.New(db).LockMFAUser(ctx, userID)
	if err != nil || locked != 1 {
		t.Fatalf("lock MFA user rows = %d, error = %v", locked, err)
	}

	// When a TOTP factor is created, read, updated, and deleted through qmark SQL.
	_, err = db.Exec(
		"INSERT INTO mfa_totp (user_id, encrypted_seed) VALUES (?, ?)",
		userID, []byte("encrypted-seed"),
	)
	if err != nil {
		t.Fatalf("create TOTP: %v", err)
	}
	var step int64
	err = db.QueryRow("SELECT COALESCE(last_accepted_step, 0) FROM mfa_totp WHERE user_id = ?", userID).
		Scan(&step)
	if err != nil || step != 0 {
		t.Fatalf("read TOTP step = %d, error = %v", step, err)
	}
	if _, err := db.Exec(
		"UPDATE mfa_totp SET last_accepted_step = ? WHERE user_id = ?",
		1,
		userID,
	); err != nil {
		t.Fatalf("update TOTP: %v", err)
	}
	if _, err := db.Exec(
		"DELETE FROM mfa_totp WHERE user_id = ?",
		userID,
	); err != nil {
		t.Fatalf("delete TOTP: %v", err)
	}

	// Given binary WebAuthn fields and a fresh browser session.
	queries := generated.New(db)
	_, err = queries.CreateSession(ctx, generated.CreateSessionParams{
		ID: "postgres-mfa-session", UserID: userID, CsrfToken: "csrf",
		ExpiresAt: 1_700_003_600,
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	credentialID := []byte{1, 2, 3}
	publicKey := []byte{4, 5, 6}
	attestationObject := []byte{7, 8, 9}
	_, err = queries.CreateWebAuthnUser(ctx, generated.CreateWebAuthnUserParams{
		UserID: userID, RpID: "example.test", UserHandle: bytes.Repeat([]byte{1}, 32),
	})
	if err != nil {
		t.Fatalf("create WebAuthn user: %v", err)
	}
	_, err = queries.CreateWebAuthnCredential(
		ctx,
		generated.CreateWebAuthnCredentialParams{
			CredentialID:                  credentialID,
			UserID:                        userID,
			Name:                          "primary",
			PublicKey:                     publicKey,
			Aaguid:                        bytes.Repeat([]byte{2}, 16),
			TransportsJson:                "[]",
			AttestationType:               "none",
			AttestationFormat:             "none",
			AttestationClientDataJson:     []byte{10},
			AttestationClientDataHash:     []byte{11},
			AttestationAuthenticatorData:  []byte{12},
			AttestationPublicKeyAlgorithm: -7,
			AttestationObject:             attestationObject,
		},
	)
	if err != nil {
		t.Fatalf("create WebAuthn credential: %v", err)
	}
	// When the credential is read and session freshness is set.
	credential, err := queries.GetWebAuthnCredentialByID(ctx, credentialID)
	if err != nil || !bytes.Equal(credential.PublicKey, publicKey) ||
		!bytes.Equal(credential.AttestationObject, attestationObject) {
		t.Fatalf(
			"binary WebAuthn credential = %#v, error = %v",
			credential,
			err,
		)
	}
	err = queries.MarkSessionReauthenticated(
		ctx,
		generated.MarkSessionReauthenticatedParams{
			ID: "postgres-mfa-session", ReauthenticatedAt: sql.NullInt64{Int64: 1_700_000_001, Valid: true},
		},
	)
	if err != nil {
		t.Fatalf("mark session reauthenticated: %v", err)
	}
	session, err := queries.GetSession(ctx, generated.GetSessionParams{
		ID: "postgres-mfa-session", ExpiresAt: 1_700_000_000,
	})
	if err != nil || !session.ReauthenticatedAt.Valid ||
		session.ReauthenticatedAt.Int64 != 1_700_000_001 {
		t.Fatalf("reauthenticated session = %#v, error = %v", session, err)
	}

	// Then duplicate rows fail, rollback preserves no row, and cascade cleanup works.
	if _, err := queries.CreateWebAuthnCredential(
		ctx,
		generated.CreateWebAuthnCredentialParams{
			CredentialID: credentialID, UserID: userID, Name: "duplicate", PublicKey: []byte{1}, TransportsJson: "[]",
		},
	); err == nil {
		t.Fatal("duplicate WebAuthn credential succeeded")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("begin transaction: %v", err)
	}
	rolledBackCredentialID := []byte{13}
	_, err = queries.WithTx(tx).
		CreateWebAuthnCredential(ctx, generated.CreateWebAuthnCredentialParams{
			CredentialID: rolledBackCredentialID, UserID: userID, Name: "rolled-back", PublicKey: []byte{1}, TransportsJson: "[]",
		})
	if err != nil {
		t.Fatalf("create transaction credential: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}
	if _, err := queries.GetWebAuthnCredentialByID(
		ctx,
		rolledBackCredentialID,
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf(
			"get rolled-back credential error = %v, want sql.ErrNoRows",
			err,
		)
	}
	if _, err := db.Exec("DELETE FROM users WHERE id = ?", userID); err != nil {
		t.Fatalf("delete MFA user: %v", err)
	}
	if _, err := queries.GetWebAuthnCredentialByID(
		ctx,
		credentialID,
	); !errors.Is(
		err,
		sql.ErrNoRows,
	) {
		t.Fatalf(
			"get cleaned WebAuthn credential error = %v, want sql.ErrNoRows",
			err,
		)
	}
}

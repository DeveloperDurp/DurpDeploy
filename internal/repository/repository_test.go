package repository_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func TestVariables_EncryptedAtRest(t *testing.T) {
	repo := newTestRepo(t)

	key := make([]byte, 32)
	box, err := secret.NewBox(key)
	if err != nil {
		t.Fatalf("NewBox: %v", err)
	}
	repo.SetSecretBox(box)

	ctx := context.Background()
	proj, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "secret-proj",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	const plaintext = "s3cr3t-token-value"
	created, err := repo.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID: proj.ID,
		Name:      "API_TOKEN",
		Value:     sql.NullString{String: plaintext, Valid: true},
		Secret:    1,
	})
	if err != nil {
		t.Fatalf("CreateVariable: %v", err)
	}
	if created.Value.String != plaintext {
		t.Fatalf(
			"wrapper should return plaintext, got %q",
			created.Value.String,
		)
	}

	raw, err := repo.Queries.GetVariable(ctx, created.ID)
	if err != nil {
		t.Fatalf("raw GetVariable: %v", err)
	}
	if raw.Value.String == plaintext {
		t.Fatalf("raw DB row must not contain the plaintext value")
	}
	if strings.Contains(raw.Value.String, plaintext) {
		t.Fatalf("raw DB row must not contain the plaintext substring")
	}

	decrypted, err := repo.GetVariable(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetVariable: %v", err)
	}
	if decrypted.Value.String != plaintext {
		t.Fatalf("expected decrypted plaintext, got %q", decrypted.Value.String)
	}

	listed, err := repo.ListVariablesByProject(ctx, proj.ID)
	if err != nil {
		t.Fatalf("ListVariablesByProject: %v", err)
	}
	if len(listed) != 1 || listed[0].Value.String != plaintext {
		t.Fatalf("ListVariablesByProject did not decrypt: %+v", listed)
	}
}

func TestVariables_NoSecretBoxIsPlaintextPassthrough(t *testing.T) {
	repo := newTestRepo(t)
	ctx := context.Background()

	proj, err := repo.Queries.CreateProject(ctx, db.CreateProjectParams{
		Name: "plain-proj",
	})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}

	created, err := repo.CreateVariable(ctx, db.CreateVariableParams{
		ProjectID: proj.ID,
		Name:      "PLAIN",
		Value:     sql.NullString{String: "plain-value", Valid: true},
	})
	if err != nil {
		t.Fatalf("CreateVariable: %v", err)
	}

	raw, err := repo.Queries.GetVariable(ctx, created.ID)
	if err != nil {
		t.Fatalf("raw GetVariable: %v", err)
	}
	if raw.Value.String != "plain-value" {
		t.Fatalf(
			"expected plaintext passthrough without a box, got %q",
			raw.Value.String,
		)
	}
}

func TestRepository_WithTx_rollsBackAllWritesWhenCallbackFails(t *testing.T) {
	// Given: a migrated repository and a transaction callback that returns an error.
	repo := newTestRepo(t)
	ctx := context.Background()
	wantErr := errors.New("stop transaction")

	// When: the callback writes a user and then fails.
	err := repo.WithTx(ctx, func(queries *db.Queries) error {
		_, err := queries.CreateUser(ctx, db.CreateUserParams{
			Email: "rollback@example.com", PasswordHash: "hash", Name: "Rollback", Role: "admin",
		})
		if err != nil {
			return err
		}
		return wantErr
	})

	// Then: the callback error survives and the write is not committed.
	if !errors.Is(err, wantErr) {
		t.Fatalf("WithTx error = %v, want %v", err, wantErr)
	}
	_, err = repo.Queries.GetUserByEmail(ctx, "rollback@example.com")
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("rolled back user lookup error = %v, want sql.ErrNoRows", err)
	}
}

func newTestRepo(t *testing.T) *repository.Repository {
	t.Helper()
	conn, err := migrate.Run(
		"file:" + t.TempDir() + "/repository.db?_pragma=foreign_keys(1)" +
			"&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
	)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return repository.New(conn)
}

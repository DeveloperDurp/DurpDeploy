package repository_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/secret"
)

func newTestRepo(t *testing.T) *repository.Repository {
	t.Helper()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return repository.New(conn)
}

// TestVariables_EncryptedAtRest verifies the acceptance criterion from
// P1-3: raw column reads via a fresh *db.Queries (i.e. what
// `sqlite3 durpdeploy.db` would see) return ciphertext only, while the
// Repository wrapper transparently returns the plaintext.
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
		t.Fatalf("wrapper should return plaintext, got %q", created.Value.String)
	}

	// Inspect the raw row exactly like `sqlite3 durpdeploy.db 'select * from
	// variables'` would — no encryption-aware wrapper involved.
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

	// The Repository wrapper decrypts transparently.
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

// TestVariables_NoSecretBoxIsPlaintextPassthrough documents that when no
// box is configured (e.g. most existing tests), values pass through
// unchanged rather than erroring.
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
		t.Fatalf("expected plaintext passthrough without a box, got %q", raw.Value.String)
	}
}

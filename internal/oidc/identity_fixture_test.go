package oidc

import (
	"context"
	"path/filepath"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

type identityLink struct {
	Identity ClaimIdentity
	UserID   int64
}

type identitySession struct {
	UserID int64
	ID     string
}

func newIdentityRepository(t *testing.T) *repository.Repository {
	t.Helper()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate database: %v", err)
	}
	conn.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = conn.Close() })
	return repository.New(conn)
}

func newConcurrentIdentityRepositories(
	t *testing.T,
) (*repository.Repository, *repository.Repository) {
	t.Helper()
	dsn := "file:" + filepath.Join(t.TempDir(), "identity.db") +
		"?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"
	first, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate first database handle: %v", err)
	}
	second, err := migrate.Run(dsn)
	if err != nil {
		first.Close()
		t.Fatalf("migrate second database handle: %v", err)
	}
	first.SetMaxOpenConns(1)
	second.SetMaxOpenConns(1)
	t.Cleanup(func() {
		_ = first.Close()
		_ = second.Close()
	})
	return repository.New(first), repository.New(second)
}

func seedIdentityUser(
	t *testing.T,
	repo *repository.Repository,
	params db.CreateUserParams,
) db.User {
	t.Helper()
	user, err := repo.Queries.CreateUser(context.Background(), params)
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return user
}

func linkIdentity(
	t *testing.T,
	repo *repository.Repository,
	link identityLink,
) {
	t.Helper()
	if _, err := repo.Queries.CreateOIDCIdentity(
		context.Background(),
		db.CreateOIDCIdentityParams{
			Issuer:  link.Identity.Issuer,
			Subject: link.Identity.Subject,
			UserID:  link.UserID,
		},
	); err != nil {
		t.Fatalf("link identity: %v", err)
	}
}

func seedIdentitySession(
	t *testing.T,
	repo *repository.Repository,
	session identitySession,
) {
	t.Helper()
	if _, err := repo.Queries.CreateSession(
		context.Background(),
		db.CreateSessionParams{
			ID: session.ID, UserID: session.UserID,
			CsrfToken: "csrf", ExpiresAt: 2_000_000_000,
		},
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}
}

func countOIDCIdentities(t *testing.T, repo *repository.Repository) int {
	t.Helper()
	var count int
	if err := repo.DB.QueryRowContext(
		context.Background(), "SELECT COUNT(*) FROM oidc_identities",
	).Scan(&count); err != nil {
		t.Fatalf("count OIDC identities: %v", err)
	}
	return count
}

func testIdentity() ClaimIdentity {
	return ClaimIdentity{
		Issuer: "https://id.example", Subject: "subject-123",
		Email: "person@example.com", Name: "Person", Role: RoleViewer,
	}
}

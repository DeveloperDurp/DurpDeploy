package db_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
)

// ponytail: returns (*db.Queries, *sql.DB) so most callers can drop the raw
// conn with `_`; the cascade test needs ExecContext to drive a user delete
// (we don't expose DeleteUser yet — that lands with the admin CLI in P0-6).
func newAuthTestDB(t *testing.T) (*db.Queries, *sql.DB) {
	t.Helper()
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { conn.Close() })
	return db.New(conn), conn
}

func TestUsers_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	q, _ := newAuthTestDB(t)

	created, err := q.CreateUser(ctx, db.CreateUserParams{
		Email:        "a@b.com",
		PasswordHash: "fakehash",
		Name:         "Alice",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("user id should not be zero")
	}
	if created.LastLoginAt.Valid {
		t.Fatal("last_login_at should start NULL")
	}

	byEmail, err := q.GetUserByEmail(ctx, "a@b.com")
	if err != nil {
		t.Fatalf("get by email: %v", err)
	}
	if byEmail.ID != created.ID {
		t.Fatalf("id mismatch: got %d want %d", byEmail.ID, created.ID)
	}
	if byEmail.Role != "admin" {
		t.Fatalf("role: got %q", byEmail.Role)
	}

	byID, err := q.GetUserByID(ctx, created.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if byID.Email != "a@b.com" {
		t.Fatalf("email mismatch: got %q", byID.Email)
	}
}

func TestUsers_UniqueEmail(t *testing.T) {
	ctx := context.Background()
	q, _ := newAuthTestDB(t)

	if _, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "dup@b.com", PasswordHash: "h1", Name: "A", Role: "admin",
	}); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "dup@b.com", PasswordHash: "h2", Name: "B", Role: "admin",
	}); err == nil {
		t.Fatal("expected unique email violation, got nil")
	}
}

func TestUsers_RoleValues(t *testing.T) {
	ctx := context.Background()
	q, _ := newAuthTestDB(t)

	if _, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "ok@b.com", PasswordHash: "h", Name: "A", Role: "admin",
	}); err != nil {
		t.Fatalf("admin insert: %v", err)
	}
	if _, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "bad@b.com", PasswordHash: "h", Name: "B", Role: "garbage",
	}); err == nil {
		t.Fatal("expected CHECK constraint violation on role='garbage'")
	}
}

func TestSessions_CreateAndGet(t *testing.T) {
	ctx := context.Background()
	q, _ := newAuthTestDB(t)

	now := int64(1_700_000_000)
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "s@b.com", PasswordHash: "h", Name: "S", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:        "abc123",
		UserID:    user.ID,
		CsrfToken: "def456",
		ExpiresAt: now + 3600,
		IpAddress: sql.NullString{String: "127.0.0.1", Valid: true},
		UserAgent: sql.NullString{String: "ua", Valid: true},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	row, err := q.GetSession(ctx, db.GetSessionParams{
		ID:        "abc123",
		ExpiresAt: now,
	})
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.UserID != user.ID {
		t.Fatalf("user_id: got %d want %d", row.UserID, user.ID)
	}
	if row.Email != "s@b.com" {
		t.Fatalf("email: got %q", row.Email)
	}
	if row.Name != "S" {
		t.Fatalf("name: got %q", row.Name)
	}
	if row.Role != "admin" {
		t.Fatalf("role: got %q", row.Role)
	}
	if row.CsrfToken != "def456" {
		t.Fatalf("csrf: got %q", row.CsrfToken)
	}
	if row.ExpiresAt != now+3600 {
		t.Fatalf("expires_at: got %d want %d", row.ExpiresAt, now+3600)
	}
}

func TestSessions_ExpiredNotReturned(t *testing.T) {
	ctx := context.Background()
	q, _ := newAuthTestDB(t)

	now := int64(1_700_000_000)
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "e@b.com", PasswordHash: "h", Name: "E", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:        "abc123",
		UserID:    user.ID,
		CsrfToken: "def456",
		ExpiresAt: now - 1,
		IpAddress: sql.NullString{Valid: false},
		UserAgent: sql.NullString{Valid: false},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := q.GetSession(ctx, db.GetSessionParams{
		ID:        "abc123",
		ExpiresAt: now,
	}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestSessions_TouchExtends(t *testing.T) {
	ctx := context.Background()
	q, _ := newAuthTestDB(t)

	now := int64(1_700_000_000)
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "t@b.com", PasswordHash: "h", Name: "T", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:        "abc123",
		UserID:    user.ID,
		CsrfToken: "def456",
		ExpiresAt: now + 60,
		IpAddress: sql.NullString{Valid: false},
		UserAgent: sql.NullString{Valid: false},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if err := q.TouchSession(ctx, db.TouchSessionParams{
		ExpiresAt: now + 3600,
		ID:        "abc123",
	}); err != nil {
		t.Fatalf("touch: %v", err)
	}

	row, err := q.GetSession(ctx, db.GetSessionParams{
		ID:        "abc123",
		ExpiresAt: now,
	})
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if row.ExpiresAt != now+3600 {
		t.Fatalf("expires_at: got %d want %d", row.ExpiresAt, now+3600)
	}
}

func TestSessions_DeleteExpired(t *testing.T) {
	ctx := context.Background()
	q, _ := newAuthTestDB(t)

	now := int64(1_700_000_000)
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "d@b.com", PasswordHash: "h", Name: "D", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	for _, sid := range []string{"expired", "valid"} {
		exp := now - 100
		if sid == "valid" {
			exp = now + 3600
		}
		if _, err := q.CreateSession(ctx, db.CreateSessionParams{
			ID:        sid,
			UserID:    user.ID,
			CsrfToken: "csrf-" + sid,
			ExpiresAt: exp,
			IpAddress: sql.NullString{Valid: false},
			UserAgent: sql.NullString{Valid: false},
		}); err != nil {
			t.Fatalf("create %s: %v", sid, err)
		}
	}

	if err := q.DeleteExpiredSessions(ctx, now); err != nil {
		t.Fatalf("delete expired: %v", err)
	}

	if _, err := q.GetSession(ctx, db.GetSessionParams{ID: "valid", ExpiresAt: now}); err != nil {
		t.Fatalf("valid session should remain: %v", err)
	}
	if _, err := q.GetSession(ctx, db.GetSessionParams{ID: "expired", ExpiresAt: now}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("expired session should be gone, got %v", err)
	}
}

func TestSessions_DeleteCascadeOnUserDelete(t *testing.T) {
	ctx := context.Background()
	q, dbConn := newAuthTestDB(t)

	now := int64(1_700_000_000)
	user, err := q.CreateUser(ctx, db.CreateUserParams{
		Email: "c@b.com", PasswordHash: "h", Name: "C", Role: "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	if _, err := q.CreateSession(ctx, db.CreateSessionParams{
		ID:        "casc1",
		UserID:    user.ID,
		CsrfToken: "cscsrf",
		ExpiresAt: now + 3600,
		IpAddress: sql.NullString{Valid: false},
		UserAgent: sql.NullString{Valid: false},
	}); err != nil {
		t.Fatalf("create session: %v", err)
	}

	if _, err := q.GetSession(ctx, db.GetSessionParams{ID: "casc1", ExpiresAt: now}); err != nil {
		t.Fatalf("pre-delete get: %v", err)
	}

	if _, err := dbConn.ExecContext(ctx, "DELETE FROM users WHERE id = ?", user.ID); err != nil {
		t.Fatalf("delete user: %v", err)
	}

	if _, err := q.GetSession(ctx, db.GetSessionParams{ID: "casc1", ExpiresAt: now}); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("session should be gone after user delete (CASCADE), got %v", err)
	}
}

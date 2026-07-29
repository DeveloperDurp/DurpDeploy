package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

func TestMintApiToken_Format(t *testing.T) {
	full, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint api token: %v", err)
	}
	if !strings.HasPrefix(full, "ddp_pat_") {
		t.Fatalf("full token %q does not start with ddp_pat_", full)
	}
	if len(full) != 72 {
		t.Fatalf("expected full token length 72, got %d", len(full))
	}
	if len(prefix) != 12 {
		t.Fatalf("expected prefix length 12, got %d", len(prefix))
	}
	if prefix != full[:12] {
		t.Fatalf("prefix %q != full[:12] %q", prefix, full[:12])
	}
	if len(hash) != 64 {
		t.Fatalf("expected hash length 64, got %d", len(hash))
	}
}

func TestMintApiToken_DifferentCallsDiffer(t *testing.T) {
	full1, _, _, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("first mint: %v", err)
	}
	full2, _, _, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("second mint: %v", err)
	}
	if full1 == full2 {
		t.Fatal("two minted tokens are identical")
	}
}

func TestHashApiToken_Stable(t *testing.T) {
	input := "ddp_pat_" + strings.Repeat("ab", 32)
	h1 := auth.HashApiToken(input)
	h2 := auth.HashApiToken(input)
	if h1 != h2 {
		t.Fatalf("hash not stable: %q vs %q", h1, h2)
	}
	sum := sha256.Sum256([]byte(input[8:]))
	expected := hex.EncodeToString(sum[:])
	if h1 != expected {
		t.Fatalf("hash %q != expected %q", h1, expected)
	}
}

func TestHashApiToken_DifferentInputsDiffer(t *testing.T) {
	a := "ddp_pat_" + strings.Repeat("ab", 32)
	b := "ddp_pat_" + strings.Repeat("cd", 32)
	ha := auth.HashApiToken(a)
	hb := auth.HashApiToken(b)
	if ha == hb {
		t.Fatal("hashes for different inputs are identical")
	}
}

func TestApiTokenMiddleware_NoHeader(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	mw := auth.ApiTokenMiddleware(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	rec := httptest.NewRecorder()

	mw(next).ServeHTTP(rec, req)

	if called {
		t.Fatal(
			"next handler should not be called without Authorization header",
		)
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestApiTokenMiddleware_BadPrefix(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	mw := auth.ApiTokenMiddleware(nil)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer not_a_token")
	rec := httptest.NewRecorder()

	mw(next).ServeHTTP(rec, req)

	if called {
		t.Fatal("next handler should not be called")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want json", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"error"`) {
		t.Fatalf("body missing error key: %s", body)
	}
}

func TestWriteBlockMiddleware_ViewerBlocked(t *testing.T) {
	tests := []struct {
		method string
		block  bool
	}{
		{http.MethodGet, false},
		{http.MethodHead, false},
		{http.MethodPost, true},
		{http.MethodPut, true},
		{http.MethodPatch, true},
		{http.MethodDelete, true},
	}

	viewer := &db.User{
		ID:    1,
		Email: "viewer@example.com",
		Name:  "Viewer",
		Role:  "viewer",
	}
	admin := &db.User{
		ID:    2,
		Email: "admin@example.com",
		Name:  "Admin",
		Role:  "admin",
	}

	for _, tc := range tests {
		t.Run(tc.method+"_viewer", func(t *testing.T) {
			called := false
			next := http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					called = true
				},
			)

			req := httptest.NewRequest(tc.method, "/api/test", nil)
			req = auth.SetUser(req, viewer)
			rec := httptest.NewRecorder()

			auth.WriteBlockMiddleware()(next).ServeHTTP(rec, req)

			if tc.block {
				if called {
					t.Fatal(
						"next handler should not be called for viewer write",
					)
				}
				if rec.Code != http.StatusForbidden {
					t.Fatalf(
						"status = %d, want %d",
						rec.Code,
						http.StatusForbidden,
					)
				}
				body, _ := io.ReadAll(rec.Body)
				if !strings.Contains(
					string(body),
					"viewers cannot perform write operations",
				) {
					t.Fatalf("body = %q, want viewer block message", body)
				}
			} else {
				if !called {
					t.Fatal("next handler should be called for viewer read")
				}
			}
		})
	}

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method+"_admin", func(t *testing.T) {
			called := false
			next := http.HandlerFunc(
				func(w http.ResponseWriter, r *http.Request) {
					called = true
				},
			)

			req := httptest.NewRequest(method, "/api/test", nil)
			req = auth.SetUser(req, admin)
			rec := httptest.NewRecorder()

			auth.WriteBlockMiddleware()(next).ServeHTTP(rec, req)

			if !called {
				t.Fatal("next handler should be called for admin write")
			}
		})
	}
}

func TestApiTokenMiddleware_Revoked(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	dsn := fmt.Sprintf(
		"file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)",
		dbPath,
	)
	conn, err := migrate.Run(dsn)
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() {
		_ = conn.Close()
		_ = os.RemoveAll(dir)
	})
	repo := repository.New(conn)

	pwHash, err := auth.HashPassword("testpass")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	user, err := repo.Queries.CreateUser(ctx, db.CreateUserParams{
		Email:        "api@example.com",
		PasswordHash: pwHash,
		Name:         "API User",
		Role:         "admin",
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}

	full, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}

	tokenID := make([]byte, 16)
	if _, err := rand.Read(tokenID); err != nil {
		t.Fatalf("generate token id: %v", err)
	}
	tokenIDStr := hex.EncodeToString(tokenID)

	if _, err := repo.Queries.CreateApiToken(ctx, db.CreateApiTokenParams{
		ID:          tokenIDStr,
		UserID:      user.ID,
		Name:        "test token",
		TokenPrefix: prefix,
		TokenHash:   hash,
		Scope:       "global",
		ExpiresAt:   sql.NullInt64{Valid: false},
	}); err != nil {
		t.Fatalf("create api token: %v", err)
	}

	if err := repo.Queries.RevokeApiToken(ctx, tokenIDStr); err != nil {
		t.Fatalf("revoke api token: %v", err)
	}

	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	mw := auth.ApiTokenMiddleware(repo)
	req := httptest.NewRequest(http.MethodGet, "/api/test", nil)
	req.Header.Set("Authorization", "Bearer "+full)
	rec := httptest.NewRecorder()

	mw(next).ServeHTTP(rec, req)

	if called {
		t.Fatal("next handler should not be called")
	}
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q, want json", ct)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), `"error"`) {
		t.Fatalf("body missing error key: %s", body)
	}
}

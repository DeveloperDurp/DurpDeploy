package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/handler/api"
	"durpdeploy/internal/migrate"
	"durpdeploy/internal/repository"
)

type tokenHarness struct {
	repo *repository.Repository
	h    *api.APITokenHandler
}

func newTokenHarness(t *testing.T) *tokenHarness {
	conn, err := migrate.Run(":memory:?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	repo := repository.New(conn)
	h := api.NewAPITokenHandler(repo)
	return &tokenHarness{repo: repo, h: h}
}

func seedUser(
	t *testing.T,
	repo *repository.Repository,
	email, role string,
) *db.User {
	pwHash, err := auth.HashPassword("testpass")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	u, err := repo.Queries.CreateUser(context.Background(), db.CreateUserParams{
		Email:        email,
		PasswordHash: pwHash,
		Name:         "Test User",
		Role:         role,
	})
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return &u
}

func seedToken(
	t *testing.T,
	repo *repository.Repository,
	userID int64,
) db.ApiToken {
	full, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatalf("mint token: %v", err)
	}
	_ = full

	id := uuid.NewString()
	row, err := repo.Queries.CreateApiToken(
		context.Background(),
		db.CreateApiTokenParams{
			ID:          id,
			UserID:      userID,
			Name:        "test token",
			TokenPrefix: prefix,
			TokenHash:   hash,
			Scope:       "global",
			ExpiresAt:   sql.NullInt64{Valid: false},
		},
	)
	if err != nil {
		t.Fatalf("create api token: %v", err)
	}
	return row
}

func withUser(r *http.Request, u *db.User) *http.Request {
	return auth.SetUser(r, u)
}

func withURLParam(r *http.Request, key, value string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(key, value)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestCreateToken_Happy(t *testing.T) {
	h := newTokenHarness(t)
	u := seedUser(t, h.repo, "owner@example.com", "admin")

	body := strings.NewReader(`{"name":"my token"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", body)
	req = withUser(req, u)
	rec := httptest.NewRecorder()

	h.h.CreateToken(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if _, ok := resp["id"]; !ok {
		t.Fatal("response missing id field")
	}
	if _, ok := resp["token_hash"]; ok {
		t.Fatal("response should not contain token_hash")
	}
	if resp["name"] != "my token" {
		t.Fatalf("expected name my token, got %v", resp["name"])
	}
	if !strings.HasPrefix(resp["id"].(string), "ddp_pat_") {
		t.Fatalf("expected id to be full plaintext token, got %v", resp["id"])
	}
	if len(resp["prefix"].(string)) != 12 {
		t.Fatalf(
			"expected prefix length 12, got %d",
			len(resp["prefix"].(string)),
		)
	}
	if resp["created_at"].(float64) <= 0 {
		t.Fatal("expected positive created_at")
	}
}

func TestCreateToken_EmptyName(t *testing.T) {
	h := newTokenHarness(t)
	u := seedUser(t, h.repo, "owner@example.com", "admin")

	body := strings.NewReader(`{"name":""}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tokens", body)
	req = withUser(req, u)
	rec := httptest.NewRecorder()

	h.h.CreateToken(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", rec.Code)
	}
}

func TestListTokens_Empty(t *testing.T) {
	h := newTokenHarness(t)
	u := seedUser(t, h.repo, "owner@example.com", "admin")

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	req = withUser(req, u)
	rec := httptest.NewRecorder()

	h.h.ListTokens(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	resp := decodeListBody(t, rec.Body.Bytes())
	if len(resp) != 0 {
		t.Fatalf("expected empty array, got %v", resp)
	}
}

func TestRevokeToken_Own(t *testing.T) {
	h := newTokenHarness(t)
	u := seedUser(t, h.repo, "owner@example.com", "admin")
	token := seedToken(t, h.repo, u.ID)

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/tokens/"+token.ID,
		nil,
	)
	req = withUser(req, u)
	req = withURLParam(req, "id", token.ID)
	rec := httptest.NewRecorder()

	h.h.RevokeToken(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestRevokeToken_Other(t *testing.T) {
	h := newTokenHarness(t)
	owner := seedUser(t, h.repo, "owner@example.com", "admin")
	attacker := seedUser(t, h.repo, "attacker@example.com", "admin")
	token := seedToken(t, h.repo, owner.ID)

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/tokens/"+token.ID,
		nil,
	)
	req = withUser(req, attacker)
	req = withURLParam(req, "id", token.ID)
	rec := httptest.NewRecorder()

	h.h.RevokeToken(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", rec.Code)
	}
}

func TestListAllTokens_Admin(t *testing.T) {
	h := newTokenHarness(t)
	admin := seedUser(t, h.repo, "admin@example.com", "admin")
	token := seedToken(t, h.repo, admin.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/tokens", nil)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.h.ListAllTokens(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	resp := decodeListBody(t, rec.Body.Bytes())
	if len(resp) != 1 {
		t.Fatalf("expected 1 token, got %d", len(resp))
	}
	if resp[0]["id"] != token.ID {
		t.Fatalf("expected token id %s, got %v", token.ID, resp[0]["id"])
	}
}

func TestListAllTokens_FilterByUserID(t *testing.T) {
	h := newTokenHarness(t)
	admin := seedUser(t, h.repo, "admin@example.com", "admin")
	other := seedUser(t, h.repo, "other@example.com", "deployer")
	keep := seedToken(t, h.repo, admin.ID)
	_ = seedToken(t, h.repo, other.ID)

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/tokens?user_id="+strconv.FormatInt(admin.ID, 10),
		nil,
	)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.h.ListAllTokens(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	resp := decodeListBody(t, rec.Body.Bytes())
	if len(resp) != 1 {
		t.Fatalf("expected 1 token, got %d: %s", len(resp), rec.Body.String())
	}
	if resp[0]["id"] != keep.ID {
		t.Fatalf("expected token id %s, got %v", keep.ID, resp[0]["id"])
	}
}

func TestListAllTokens_UnknownUserID(t *testing.T) {
	h := newTokenHarness(t)
	admin := seedUser(t, h.repo, "admin@example.com", "admin")

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/tokens?user_id=99999",
		nil,
	)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.h.ListAllTokens(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestListAllTokens_InvalidUserID(t *testing.T) {
	h := newTokenHarness(t)
	admin := seedUser(t, h.repo, "admin@example.com", "admin")

	req := httptest.NewRequest(
		http.MethodGet,
		"/api/v1/admin/tokens?user_id=notanumber",
		nil,
	)
	req = withUser(req, admin)
	rec := httptest.NewRecorder()

	h.h.ListAllTokens(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRevokeAnyToken_Admin(t *testing.T) {
	h := newTokenHarness(t)
	admin := seedUser(t, h.repo, "admin@example.com", "admin")
	user := seedUser(t, h.repo, "user@example.com", "deployer")
	token := seedToken(t, h.repo, user.ID)

	req := httptest.NewRequest(
		http.MethodDelete,
		"/api/v1/admin/tokens/"+token.ID,
		nil,
	)
	req = withUser(req, admin)
	req = withURLParam(req, "id", token.ID)
	rec := httptest.NewRecorder()

	h.h.RevokeAnyToken(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rec.Code)
	}
}

func TestListTokens_IncludesOwnedTokens(t *testing.T) {
	h := newTokenHarness(t)
	u := seedUser(t, h.repo, "owner@example.com", "admin")
	token := seedToken(t, h.repo, u.ID)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/tokens", nil)
	req = withUser(req, u)
	rec := httptest.NewRecorder()

	h.h.ListTokens(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	resp := decodeListBody(t, rec.Body.Bytes())
	if len(resp) != 1 {
		t.Fatalf("expected 1 token, got %d", len(resp))
	}
	if resp[0]["id"] != token.ID {
		t.Fatalf("expected token id %s, got %v", token.ID, resp[0]["id"])
	}
	if _, ok := resp[0]["token_hash"]; ok {
		t.Fatal("list response should not contain token_hash")
	}
}

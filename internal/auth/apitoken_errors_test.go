package auth_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/testutil"
)

func TestApiTokenMiddlewareLookupErrors(t *testing.T) {
	for _, scenario := range []string{
		"unknown", "revoked", "expired", "invalid-scope",
		"closed-database", "canceled", "deadline",
		"scoped-transaction",
	} {
		t.Run(scenario, func(t *testing.T) {
			repo := newAccessTestRepo(t)
			user := seedUser(t, repo, "token-errors@example.test", "admin")
			token := seedLookupErrorToken(t, repo, user.ID, scenario)
			request := httptest.NewRequest("GET", "/api/v1/users/me", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			status, message := 401, "invalid or revoked token"
			switch scenario {
			case "revoked":
				if err := repo.Queries.RevokeApiToken(
					t.Context(), "token-error",
				); err != nil {
					t.Fatal(err)
				}
			case "closed-database":
				testutil.Close(t, repo.DB)
				status, message = 503, "internal server error"
			case "scoped-transaction":
				// Keep the lookup connection live, but fail transaction setup.
				closed, err := sql.Open("sqlite", ":memory:")
				if err != nil {
					t.Fatal(err)
				}
				testutil.Close(t, closed)
				repo.DB = closed
				status, message = 503, "internal server error"
			case "canceled", "deadline":
				var ctx context.Context
				var cancel context.CancelFunc
				if scenario == "canceled" {
					ctx, cancel = context.WithCancel(request.Context())
				} else {
					ctx, cancel = context.WithDeadline(
						request.Context(), time.Now().Add(-time.Second),
					)
				}
				cancel()
				request = request.WithContext(ctx)
				status, message = 503, "internal server error"
			}
			response := httptest.NewRecorder()
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("failed authentication reached protected handler")
			})
			auth.ApiTokenMiddleware(repo)(next).ServeHTTP(response, request)
			if response.Code != status ||
				response.Body.String() != "{\"error\":\""+message+"\"}\n" {
				t.Fatalf("response=%d %s", response.Code, response.Body)
			}
		})
	}
}

func seedLookupErrorToken(
	t *testing.T, repo *repository.Repository, userID int64, scenario string,
) string {
	t.Helper()
	token, prefix, hash, err := auth.MintApiToken()
	if err != nil {
		t.Fatal(err)
	}
	if scenario == "unknown" {
		return token
	}
	expiry := sql.NullInt64{}
	if scenario == "expired" {
		expiry = sql.NullInt64{Int64: 1, Valid: true}
	}
	scope := "global"
	if scenario == "invalid-scope" || scenario == "scoped-transaction" {
		scope = "scoped" // No selected projects is invalid.
	}
	_, err = repo.Queries.CreateApiToken(t.Context(), db.CreateApiTokenParams{
		ID: "token-error", UserID: userID, Name: scenario,
		TokenPrefix: prefix, TokenHash: hash, Scope: scope, ExpiresAt: expiry,
	})
	if err != nil {
		t.Fatal(err)
	}
	return token
}

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// MintApiToken generates a cryptographically random API token.
// Returns the full token string (ddp_pat_<64hex>), the prefix (first 12 chars),
// and the SHA-256 hash of the random portion for database storage.
func MintApiToken() (full, prefix, hash string, err error) {
	randBytes := make([]byte, 32)
	if _, err := rand.Read(randBytes); err != nil {
		return "", "", "", fmt.Errorf("generate token: %w", err)
	}
	hexPart := hex.EncodeToString(randBytes) // 64 hex chars
	full = "ddp_pat_" + hexPart
	prefix = full[:12] // "ddp_pat_" is 8 chars + 4 hex chars = 12
	sum := sha256.Sum256([]byte(hexPart))
	hash = hex.EncodeToString(sum[:])
	return full, prefix, hash, nil
}

// HashApiToken returns the SHA-256 hex hash of the random portion of a token.
// Input must be "ddp_pat_<64hex>".
func HashApiToken(plaintext string) string {
	// Strip the "ddp_pat_" prefix.
	hexPart := plaintext[8:]
	sum := sha256.Sum256([]byte(hexPart))
	return hex.EncodeToString(sum[:])
}

// RenderJSONError writes a JSON error response with the given status and message.
func RenderJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// WriteBlockMiddleware blocks viewers from performing write operations.
// Returns 403 JSON with {"error":"viewers cannot perform write operations"}.
// This is the API counterpart to the CSRFMiddleware's viewer block.
func WriteBlockMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodPost,
				http.MethodPut,
				http.MethodPatch,
				http.MethodDelete:
				if RoleFromContext(r.Context()) == "viewer" {
					RenderJSONError(
						w,
						http.StatusForbidden,
						"viewers cannot perform write operations",
					)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ApiTokenMiddleware reads a Bearer token from the Authorization header,
// validates it against stored API token hashes, and injects the user into the
// request context. Any failure returns 401 JSON.
func ApiTokenMiddleware(
	repo *repository.Repository,
) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			header := r.Header.Get("Authorization")
			if header == "" {
				RenderJSONError(w, http.StatusUnauthorized, "unauthenticated")
				return
			}

			parts := strings.SplitN(header, " ", 2)
			if len(parts) != 2 || parts[0] != "Bearer" {
				RenderJSONError(
					w,
					http.StatusUnauthorized,
					"invalid authorization format",
				)
				return
			}

			token := parts[1]
			if !strings.HasPrefix(token, "ddp_pat_") || len(token) <= 8 {
				RenderJSONError(
					w,
					http.StatusUnauthorized,
					"invalid token format",
				)
				return
			}

			hash := HashApiToken(token)

			row, err := repo.Queries.GetApiTokenByHash(
				r.Context(),
				db.GetApiTokenByHashParams{
					TokenHash: hash,
					ExpiresAt: sql.NullInt64{
						Int64: time.Now().Unix(),
						Valid: true,
					},
				},
			)
			if err != nil {
				RenderJSONError(
					w,
					http.StatusUnauthorized,
					"invalid or revoked token",
				)
				return
			}

			user := &db.User{
				ID:    row.UserID,
				Email: row.Email,
				Name:  row.UserName,
				Role:  row.Role,
			}
			r = SetUser(r, user)

			go func() {
				_ = repo.Queries.TouchApiTokenLastUsed(
					context.Background(),
					db.TouchApiTokenLastUsedParams{
						LastUsedAt: sql.NullInt64{
							Int64: time.Now().Unix(),
							Valid: true,
						},
						ID: row.ID,
					},
				)
			}()

			next.ServeHTTP(w, r)
		})
	}
}

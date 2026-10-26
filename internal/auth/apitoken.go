package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

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
// Malformed tokens return an empty string.
func HashApiToken(plaintext string) string {
	hexPart, ok := parseApiToken(plaintext)
	if !ok {
		return ""
	}
	sum := sha256.Sum256([]byte(hexPart))
	return hex.EncodeToString(sum[:])
}

func parseApiToken(token string) (string, bool) {
	if len(token) != 72 || !strings.HasPrefix(token, "ddp_pat_") {
		return "", false
	}
	suffix := token[8:]
	if _, err := hex.DecodeString(suffix); err != nil {
		return "", false
	}
	return suffix, true
}

// RenderJSONError writes a JSON error response with the given status and message.
func RenderJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// RenderRequestError preserves the API envelope on shared API/web paths.
func RenderRequestError(
	w http.ResponseWriter, r *http.Request, status int, msg string,
) {
	if strings.HasPrefix(r.URL.Path, "/api/v1/") {
		RenderJSONError(w, status, msg)
		return
	}
	http.Error(w, msg, status)
}

// WriteBlockMiddleware separates reads, deployment execution, and authoring.
// It preserves Viewer errors and returns JSON for restricted Deployer writes.
func WriteBlockMiddleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost &&
				r.URL.Path == "/api/v1/users/me/password" {
				servePasswordChangeWrite(w, r, next)
				return
			}
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
				if !projectWriteAllowed(r) {
					RenderJSONError(
						w,
						http.StatusForbidden,
						"maintainer permission is required for configuration writes",
					)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

func servePasswordChangeWrite(
	w http.ResponseWriter, r *http.Request, next http.Handler,
) {
	token := TokenFromContext(r.Context())
	if token == nil || token.Scope != "global" || token.Role != "" {
		RenderJSONError(w, http.StatusForbidden,
			"an unrestricted global token is required")
		return
	}
	next.ServeHTTP(w, r)
}

// ApiTokenMiddleware reads a Bearer token from the Authorization header,
// validates it against stored API token hashes, and injects the user into the
// request context. Any failure returns 401 JSON.
func ApiTokenMiddleware(
	repo *repository.Repository,
) func(http.Handler) http.Handler {
	telemetry := apiTokenTelemetry{
		pending: make(map[string]struct{}),
		limits:  repo.ResourceLimits().TokenTelemetry,
	}
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

			hash := HashApiToken(parts[1])
			if hash == "" {
				RenderJSONError(
					w,
					http.StatusUnauthorized,
					"invalid token format",
				)
				return
			}

			row, grant, err := loadTokenAccess(r.Context(), repo, hash)
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
			r = setTokenAccess(r, row.ID, grant)

			telemetry.touch(repo, row)

			next.ServeHTTP(w, r)
		})
	}
}

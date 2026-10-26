package auth_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func TestHashApiTokenPreservesHexText(t *testing.T) {
	suffix := strings.Repeat("AB", 32)
	sum := sha256.Sum256([]byte(suffix))
	if auth.HashApiToken("ddp_pat_"+suffix) != hex.EncodeToString(
		sum[:],
	) {
		t.Fatal("valid hexadecimal token text was normalized before hashing")
	}
}

func malformedAPITokens() map[string]string {
	return map[string]string{
		"empty":              "",
		"short-prefix":       "ddp_pat",
		"wrong-prefix":       "xxx_pat_" + strings.Repeat("a", 64),
		"empty-suffix":       "ddp_pat_",
		"short-suffix":       "ddp_pat_a",
		"truncated-suffix":   "ddp_pat_" + strings.Repeat("a", 63),
		"long-suffix":        "ddp_pat_" + strings.Repeat("a", 65),
		"nonhex":             "ddp_pat_" + strings.Repeat("a", 63) + "g",
		"embedded-space":     "ddp_pat_" + strings.Repeat("a", 63) + " ",
		"embedded-zero":      "ddp_pat_" + strings.Repeat("a", 63) + "\x00",
		"non-ASCII":          "ddp_pat_" + strings.Repeat("a", 62) + "é",
		"leading-whitespace": " ddp_pat_" + strings.Repeat("a", 64),
	}
}

func TestHashApiTokenRejectsInvalidFormat(t *testing.T) {
	for name, token := range malformedAPITokens() {
		t.Run(name, func(t *testing.T) {
			if auth.HashApiToken(token) != "" {
				t.Fatal("malformed token produced a hash")
			}
		})
	}
}

func TestApiTokenFormatRejectsBeforeLookup(t *testing.T) {
	for name, token := range malformedAPITokens() {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest("GET", "/api/v1/projects", nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("malformed token reached protected handler")
			})
			// A nil repository makes any attempted database access fail.
			auth.ApiTokenMiddleware(nil)(next).ServeHTTP(response, request)
			var body map[string]string
			if response.Code != http.StatusUnauthorized ||
				json.Unmarshal(response.Body.Bytes(), &body) != nil ||
				body["error"] != "invalid token format" {
				t.Fatalf("response=%d %s", response.Code, response.Body)
			}
			request = auth.SetUser(request, &db.User{ID: 1, Role: "admin"})
			if auth.CanStreamDeployment(request, nil, 1) {
				t.Fatal("malformed token retained stream access")
			}
		})
	}
}

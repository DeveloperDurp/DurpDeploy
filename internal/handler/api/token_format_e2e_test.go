//go:build e2e

package api_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

type tokenFormatDB struct {
	db.DBTX
	lookups atomic.Int64
}

func (p *tokenFormatDB) QueryRowContext(
	ctx context.Context, query string, args ...any,
) *sql.Row {
	if strings.Contains(query, "-- name: GetApiTokenByHash ") {
		p.lookups.Add(1)
	}
	return p.DBTX.QueryRowContext(ctx, query, args...)
}

func TestTokenFormatAPIWebE2E(t *testing.T) {
	f := newArtifactE2E(t, fakePodman(t))
	probe := &tokenFormatDB{DBTX: f.h.repo.DB}
	f.h.repo.Queries = db.New(probe)
	for name, token := range map[string]string{
		"wrong-prefix": "bad_pat_" + strings.Repeat("a", 64),
		"empty-suffix": "ddp_pat_",
		"short":        "ddp_pat_a",
		"truncated":    "ddp_pat_" + strings.Repeat("a", 63),
		"long":         "ddp_pat_" + strings.Repeat("a", 65),
		"nonhex":       "ddp_pat_" + strings.Repeat("a", 63) + "g",
	} {
		for _, path := range []string{
			"/api/v1/users/me",
			"/api/v1/deployments/1/logs/stream",
			"/api/v1/deployments/1/events",
		} {
			t.Run(name+path, func(t *testing.T) {
				before := probe.lookups.Load()
				tokenFormatResponse(
					t,
					f,
					token,
					path,
					401,
					"invalid token format",
				)
				if probe.lookups.Load() != before {
					t.Fatal("malformed token reached database lookup")
				}
			})
		}
	}
	before := probe.lookups.Load()
	tokenFormatResponse(t, f, f.token, "/api/v1/users/me", 200, "")
	if probe.lookups.Load() != before+1 {
		t.Fatal("valid token did not perform durable authentication")
	}
	unknown, _, _, err := auth.MintApiToken()
	if err != nil {
		t.Fatal(err)
	}
	tokenFormatResponse(
		t,
		f,
		unknown,
		"/api/v1/users/me",
		401,
		"invalid or revoked token",
	)
	if probe.lookups.Load() != before+2 {
		t.Fatal("well-formed unknown token did not reach database lookup")
	}
	f.web(t, "GET", "/projects", nil, 200)
	var id string
	if err := f.h.repo.DB.QueryRowContext(t.Context(),
		"SELECT id FROM api_tokens WHERE token_hash = ?",
		auth.HashApiToken(f.token)).Scan(&id); err != nil {
		t.Fatal(err)
	}
	f.api(t, "DELETE", "/api/v1/admin/tokens/"+id, nil, 204)
	tokenFormatResponse(
		t,
		f,
		f.token,
		"/api/v1/users/me",
		401,
		"invalid or revoked token",
	)
	// Revoking the API token does not revoke an independent browser session.
	f.web(t, "GET", "/projects", nil, 200)
}

func tokenFormatResponse(
	t *testing.T,
	f *artifactE2E,
	token, path string,
	status int,
	message string,
) {
	t.Helper()
	req, err := http.NewRequestWithContext(
		t.Context(),
		"GET",
		f.baseURL+path,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	response, err := f.client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != status || !json.Valid(body) {
		t.Fatalf("status=%d error=%v body=%s", response.StatusCode, err, body)
	}
	if message != "" {
		var envelope map[string]string
		if json.Unmarshal(body, &envelope) != nil ||
			envelope["error"] != message {
			t.Fatalf("unexpected error envelope: %s", body)
		}
	}
}

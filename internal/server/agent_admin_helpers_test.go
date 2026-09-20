package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
)

func seedAgentRouteUser(
	t *testing.T,
	h *oidcRouterHarness,
	role string,
	id string,
) {
	t.Helper()
	user, err := h.repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
		Email: id + "@test.local", Name: id, PasswordHash: "unused", Role: role,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.Queries.CreateSession(t.Context(), db.CreateSessionParams{
		ID: id, UserID: user.ID, CsrfToken: "csrf",
		ExpiresAt: time.Now().Add(time.Hour).Unix(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.Queries.CreateApiToken(t.Context(), db.CreateApiTokenParams{
		ID: id, UserID: user.ID, Name: "fixture", TokenPrefix: "fixture",
		TokenHash: auth.HashApiToken("ddp_pat_" + id), Scope: "global",
	})
	if err != nil {
		t.Fatal(err)
	}
}

func seedAssignableAgent(t *testing.T, h *oidcRouterHarness) int64 {
	t.Helper()
	environment, err := h.repo.Queries.CreateEnvironment(
		t.Context(), db.CreateEnvironmentParams{Name: "agent environment"},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.Queries.CreateAgent(t.Context(), db.CreateAgentParams{
		ID: "agent-a", Name: "Agent A", Endpoint: "https://agent.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.DB.ExecContext(t.Context(), `
INSERT INTO agent_pairings
(agent_id,pairing_code_hash,agent_public_identity,agent_pin,
 server_public_identity,server_pin,encrypted_identity,state,expires_at,paired_at)
VALUES ('agent-a', ?, 'public', ?, 'server-public', ?, 'ciphertext',
        'paired', 4102444800, unixepoch())`,
		bytes.Repeat([]byte{1}, 32), strings.Repeat("a", 64),
		strings.Repeat("b", 64))
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.DB.ExecContext(t.Context(), `
UPDATE agents SET status='active', certificate_pem='public-certificate',
certificate_fingerprint=?, encrypted_identity='ciphertext'
WHERE id='agent-a'`, strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	return environment.ID
}

func browserFormRequest(
	method string,
	path string,
	session string,
	form url.Values,
) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", form.Get("csrf_token"))
	req.AddCookie(&http.Cookie{Name: "session", Value: session})
	return req
}

func assertAuditActionCount(
	t *testing.T,
	h *oidcRouterHarness,
	action string,
	want int,
) {
	t.Helper()
	var got int
	if err := h.repo.DB.QueryRowContext(
		t.Context(),
		"SELECT count(*) FROM audit_log WHERE action = ?",
		action,
	).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("audit action %q count=%d want=%d", action, got, want)
	}
}

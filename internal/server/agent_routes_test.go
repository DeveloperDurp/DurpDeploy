package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

type agentListItem struct {
	ID                string `json:"id"`
	CertificatePem    string `json:"certificate_pem"`
	EncryptedIdentity string `json:"encrypted_identity"`
}

func TestAgentRoutesRejectBrowserSession(t *testing.T) {
	h := newOIDCRouterHarness(t)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	for _, path := range []string{
		agentproto.PollPath, agentproto.StartPath,
		agentproto.HeartbeatPath, agentproto.LogsPath, agentproto.ResultPath,
		agentproto.CancelledPath,
	} {
		path = strings.ReplaceAll(path, "{id}", "42")
		request := httptest.NewRequest(http.MethodPost, path, nil)
		request.AddCookie(&http.Cookie{Name: "session", Value: "browser"})
		request.Header.Set("X-CSRF-Token", "csrf")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != http.StatusNotFound {
			t.Fatalf(
				"direct route exposed on browser server: %s status=%d",
				path,
				response.Code,
			)
		}
		t.Logf("browser POST %s status=%d", path, response.Code)
	}
}

func TestServerAgentManagementProtection(t *testing.T) {
	h := newOIDCRouterHarness(t)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	for _, role := range []string{"admin", "deployer", "viewer"} {
		user, err := h.repo.Queries.CreateUser(
			context.Background(),
			db.CreateUserParams{
				Email: role + "@test.local", Name: role, PasswordHash: "unused", Role: role,
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.repo.Queries.CreateApiToken(
			context.Background(),
			db.CreateApiTokenParams{
				ID: role, UserID: user.ID, Name: "fixture", TokenPrefix: "fixture",
				TokenHash: auth.HashApiToken(
					"ddp_pat_" + role,
				), Scope: "global",
			},
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = h.repo.Queries.CreateSession(
			context.Background(),
			db.CreateSessionParams{
				ID: role, UserID: user.ID, CsrfToken: "csrf", ExpiresAt: time.Now().Add(time.Hour).Unix(),
			},
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, method, path, role, csrf string
		status                         int
	}{
		{"anonymous", http.MethodGet, "/admin/agents", "", "", 303},
		{"missing csrf", http.MethodPost, "/admin/agents/pair", "admin", "", 403},
		{"deployer", http.MethodGet, "/admin/agents", "deployer", "csrf", 403},
		{"viewer", http.MethodGet, "/admin/agents", "viewer", "csrf", 403},
		{"admin", http.MethodGet, "/admin/agents", "admin", "csrf", 200},
		{"missing agent", http.MethodPost, "/admin/agents/a/revoke", "admin", "csrf", 404},
		{"api rejects session", http.MethodGet, "/api/v1/admin/agents", "admin", "csrf", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, nil)
			if tc.role != "" {
				req.AddCookie(&http.Cookie{Name: "session", Value: tc.role})
			}
			req.Header.Set("X-CSRF-Token", tc.csrf)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("status=%d want=%d", res.Code, tc.status)
			}
			t.Logf(
				"%s %s role=%s status=%d",
				tc.method,
				tc.path,
				tc.role,
				res.Code,
			)
		})
	}
	var count int
	for _, role := range []string{"admin", "deployer", "viewer"} {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/agents", nil)
		req.Header.Set("Authorization", "Bearer ddp_pat_"+role)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		want := http.StatusForbidden
		if role == "admin" {
			want = http.StatusOK
		}
		if res.Code != want {
			t.Fatalf("API role=%s status=%d", role, res.Code)
		}
		t.Logf("API GET /admin/agents role=%s status=%d", role, res.Code)
	}
	if err := h.repo.DB.QueryRow("SELECT count(*) FROM audit_log").
		Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected/unavailable operations were audited as success")
	}
}

func TestNonAdminAgentAdminForbidden(t *testing.T) {
	h := newOIDCRouterHarness(t)
	router := NewRouter(h.repo, h.runner, h.parser, h.authHandler)
	user, err := h.repo.Queries.CreateUser(
		t.Context(),
		db.CreateUserParams{
			Email:        "deployer-agent-admin@test.local",
			Name:         "deployer",
			PasswordHash: "unused",
			Role:         "deployer",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.Queries.CreateSession(
		t.Context(),
		db.CreateSessionParams{
			ID:        "deployer-agent-admin",
			UserID:    user.ID,
			CsrfToken: "csrf",
			ExpiresAt: time.Now().Add(time.Hour).Unix(),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.Queries.CreateApiToken(
		t.Context(),
		db.CreateApiTokenParams{
			ID:          "deployer-agent-admin",
			UserID:      user.ID,
			Name:        "fixture",
			TokenPrefix: "fixture",
			TokenHash: auth.HashApiToken(
				"ddp_pat_deployer-agent-admin",
			),
			Scope: "global",
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("Browser", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/admin/agents", nil)
		req.AddCookie(&http.Cookie{
			Name:  "session",
			Value: "deployer-agent-admin",
		})
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("status=%d want=%d", res.Code, http.StatusForbidden)
		}
	})

	t.Run("API", func(t *testing.T) {
		req := httptest.NewRequest(
			http.MethodGet,
			"/api/v1/admin/agents",
			nil,
		)
		req.Header.Set(
			"Authorization",
			"Bearer ddp_pat_deployer-agent-admin",
		)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		if res.Code != http.StatusForbidden {
			t.Fatalf("status=%d want=%d", res.Code, http.StatusForbidden)
		}
	})
}

func TestAdminAgentListAPIRedactsCredentials(t *testing.T) {
	h := newOIDCRouterHarness(t)
	admin, err := h.repo.Queries.CreateUser(t.Context(), db.CreateUserParams{
		Email: "agent-admin@test.local", Name: "admin",
		PasswordHash: "unused", Role: "admin",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.Queries.CreateApiToken(t.Context(), db.CreateApiTokenParams{
		ID: "agent-admin", UserID: admin.ID, Name: "fixture",
		TokenPrefix: "fixture", Scope: "global",
		TokenHash: auth.HashApiToken("ddp_pat_agent-admin"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.Queries.CreateAgent(t.Context(), db.CreateAgentParams{
		ID: "agent-1", Name: "build agent", Endpoint: "https://agent.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.DB.ExecContext(t.Context(), `
UPDATE agents SET certificate_pem = 'secret-cert',
encrypted_identity = 'secret-key' WHERE id = 'agent-1'`)
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/agents", nil)
	req.Header.Set("Authorization", "Bearer ddp_pat_agent-admin")
	res := httptest.NewRecorder()
	NewRouter(h.repo, h.runner, h.parser, h.authHandler).ServeHTTP(res, req)
	if res.Code != http.StatusOK {
		t.Fatalf("status=%d want=%d body=%s", res.Code, http.StatusOK, res.Body)
	}
	var items []agentListItem
	if err := json.NewDecoder(res.Body).Decode(&items); err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 || items[0].ID != "agent-1" {
		t.Fatalf("items=%+v", items)
	}
	if items[0].CertificatePem != "" || items[0].EncryptedIdentity != "" {
		t.Fatalf("credentials leaked: %+v", items[0])
	}
}

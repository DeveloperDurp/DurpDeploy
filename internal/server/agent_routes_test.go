package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"

	agentproto "github.com/DeveloperDurp/durpdeploy-agent/protocol"
)

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
		name, path, role, csrf string
		status                 int
	}{
		{"anonymous", "/admin/agents", "", "", 303},
		{"missing csrf", "/admin/agents", "admin", "", 403},
		{"deployer", "/admin/agents", "deployer", "csrf", 403},
		{"viewer", "/admin/agents", "viewer", "csrf", 403},
		{"admin seam", "/admin/agents", "admin", "csrf", 501},
		{"nested seam", "/admin/agents/a/revoke", "admin", "csrf", 501},
		{"api rejects session", "/api/v1/admin/agents", "admin", "csrf", 401},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			if tc.role != "" {
				req.AddCookie(&http.Cookie{Name: "session", Value: tc.role})
			}
			req.Header.Set("X-CSRF-Token", tc.csrf)
			res := httptest.NewRecorder()
			router.ServeHTTP(res, req)
			if res.Code != tc.status {
				t.Fatalf("status=%d want=%d", res.Code, tc.status)
			}
			t.Logf("POST %s role=%s status=%d", tc.path, tc.role, res.Code)
		})
	}
	var count int
	for _, role := range []string{"admin", "deployer", "viewer"} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/agents", nil)
		req.Header.Set("Authorization", "Bearer ddp_pat_"+role)
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		want := http.StatusForbidden
		if role == "admin" {
			want = http.StatusNotImplemented
		}
		if res.Code != want {
			t.Fatalf("API role=%s status=%d", role, res.Code)
		}
		t.Logf("API POST /admin/agents role=%s status=%d", role, res.Code)
	}
	if err := h.repo.DB.QueryRow("SELECT count(*) FROM audit_log").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("rejected/unavailable operations were audited as success")
	}
}

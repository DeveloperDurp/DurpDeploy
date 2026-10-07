package server

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestAgentDeleteConfirmedCleanupKeepsMaintenanceAvailable(t *testing.T) {
	for _, action := range []string{"refresh", "project", "environment"} {
		t.Run(action, func(t *testing.T) {
			testAgentDeleteConfirmedCleanupMaintenance(t, action)
		})
	}
}

func testAgentDeleteConfirmedCleanupMaintenance(t *testing.T, action string) {
	t.Helper()
	// Given: confirmed remote cleanup retains its original terminal outcome.
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "delete-admin")
	environmentID := seedAssignableAgent(t, h)
	if _, err := h.repo.DB.ExecContext(t.Context(), `
	INSERT INTO projects(id,name) VALUES(1,'cleanup history');
	INSERT INTO releases(id,project_id,version,steps_json)
	VALUES(1,1,'1','[]');
	INSERT INTO deployments(id,release_id,environment_id,status,
	assigned_agent_id,finished_at)
	VALUES(1,1,?,'cleanup_unconfirmed','agent-a',200);
	INSERT INTO remote_deployment_claims
	(deployment_id,agent_id,state,claim_token_hash,ciphertext,
	claim_expires_at,last_heartbeat_at,started_at,finished_at,cleanup_confirmed_at)
	VALUES(1,'agent-a','cleanup_unconfirmed',zeroblob(32),'fixture',
	200,100,100,200,201)`, environmentID); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(NewRouter(h.repo, h.runner,
		h.parser, h.authHandler))
	t.Cleanup(srv.Close)

	// When
	res := agentDeleteRequest(t, srv, http.MethodDelete,
		"/api/v1/admin/agents/agent-a")
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status=%d", res.StatusCode)
	}

	// Then: history remains, and unrelated maintenance is not falsely blocked.
	history := agentDeleteRequest(t, srv, http.MethodGet,
		"/api/v1/deployments/1")
	if history.StatusCode != http.StatusOK {
		t.Fatalf("history status=%d", history.StatusCode)
	}
	method, path, want := http.MethodPost,
		"/api/v1/projects/1/releases/1/refresh", http.StatusOK
	if action == "project" {
		method, path, want = http.MethodDelete,
			"/api/v1/projects/1", http.StatusNoContent
	}
	if action == "environment" {
		method, path, want = http.MethodDelete,
			"/api/v1/environments/"+strconv.FormatInt(
				environmentID,
				10,
			),
			http.StatusNoContent
	}
	result := agentDeleteRequest(t, srv, method, path)
	if result.StatusCode != want {
		t.Fatalf(
			"%s status=%d want=%d",
			action,
			result.StatusCode,
			want,
		)
	}
}

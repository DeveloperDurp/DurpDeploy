package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func TestAgentDeleteConfirmedCleanupKeepsMaintenanceAvailable(t *testing.T) {
	for _, action := range []string{"refresh", "project", "environment", "rollback"} {
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
	if action == "rollback" {
		assertAgentDeleteCleanupRollback(t, h, srv)
		return
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

func assertAgentDeleteCleanupRollback(
	t *testing.T,
	h *oidcRouterHarness,
	srv *httptest.Server,
) {
	t.Helper()
	if _, err := h.repo.DB.ExecContext(t.Context(), `
	INSERT INTO releases(id,project_id,version,steps_json) VALUES(2,1,'2','[]');
	INSERT INTO deployments(id,release_id,environment_id,status,finished_at)
	SELECT 2,1,environment_id,'succeeded',300 FROM deployments WHERE id=1;
	INSERT INTO deployments(id,release_id,environment_id,status,finished_at)
	SELECT 3,2,environment_id,'succeeded',400 FROM deployments WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	res := agentDeleteRequest(
		t,
		srv,
		http.MethodGet,
		"/api/v1/deployments/3/rollback",
	)
	if res.StatusCode != http.StatusOK {
		t.Fatalf("rollback preview status=%d", res.StatusCode)
	}
	var preview struct {
		TargetDeploymentID int64 `json:"target_deployment_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&preview); err != nil ||
		preview.TargetDeploymentID != 2 {
		t.Fatalf("rollback preview=%+v error=%v", preview, err)
	}
	assertAgentDeleteCleanupRollbackExecution(t, srv)
}

func assertAgentDeleteCleanupRollbackExecution(
	t *testing.T,
	srv *httptest.Server,
) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
		srv.URL+"/api/v1/deployments/3/rollback",
		strings.NewReader(`{"target_deployment_id":2}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer ddp_pat_delete-admin")
	req.Header.Set("Content-Type", "application/json")
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusCreated {
		t.Fatalf("rollback execution status=%d", res.StatusCode)
	}
	var result struct {
		ID        int64 `json:"id"`
		ReleaseID int64 `json:"release_id"`
	}
	if err := json.NewDecoder(res.Body).Decode(&result); err != nil ||
		result.ID <= 3 ||
		result.ReleaseID != 1 {
		t.Fatalf("rollback execution=%+v error=%v", result, err)
	}
}

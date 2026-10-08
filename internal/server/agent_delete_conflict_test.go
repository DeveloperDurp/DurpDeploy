package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAgentDeleteAPIRejectsUnresolvedWork(t *testing.T) {
	for _, bufferedTable := range []string{"", "remote_step_runs", "remote_deployment_claims"} {
		for _, surface := range []string{"api", "web", "htmx"} {
			t.Run(surface+"/"+bufferedTable, func(t *testing.T) {
				testAgentDeleteConflict(t, surface, bufferedTable)
			})
		}
	}
}

func testAgentDeleteConflict(t *testing.T, surface, bufferedTable string) {
	t.Helper()
	h := newAgentDeleteConflictHarness(t, bufferedTable)
	req := httptest.NewRequest(http.MethodDelete,
		"/api/v1/admin/agents/agent-a", nil)
	if surface != "api" {
		req = httptest.NewRequest(http.MethodPost,
			"/admin/agents/agent-a/delete", nil)
		req.AddCookie(
			&http.Cookie{Name: "session", Value: "delete-admin"},
		)
		req.Header.Set("X-CSRF-Token", "csrf")
	}
	req.Header.Set("Authorization", "Bearer ddp_pat_delete-admin")
	want := http.StatusConflict
	if surface == "htmx" {
		req.Header.Set("HX-Request", "true")
		want = http.StatusOK
	}
	res := httptest.NewRecorder()

	// When
	NewRouter(
		h.repo,
		h.runner,
		h.parser,
		h.authHandler,
	).ServeHTTP(res, req)

	// Then: the public response reports the conflict and nothing changes.
	if res.Code != want {
		t.Fatalf(
			"status=%d want=%d body=%s",
			res.Code,
			want,
			res.Body,
		)
	}
	if surface == "htmx" && (!strings.Contains(
		res.Header().Get("HX-Trigger"), "makeToast",
	) || res.Header().Get("HX-Reswap") != "none") {
		t.Fatal(
			"blocked HTMX deletion did not keep the page and show a toast",
		)
	}
	agent, err := h.repo.Queries.GetAgent(t.Context(), "agent-a")
	if err != nil || agent.Status != "active" {
		t.Fatalf("agent=%+v error=%v", agent, err)
	}
	deployment, err := h.repo.Queries.GetDeployment(t.Context(), 1)
	if err != nil || deployment.Status != "running" {
		t.Fatalf("deployment=%+v error=%v", deployment, err)
	}
	assertAuditActionCount(t, h, "delete_agent", 0)
}

func newAgentDeleteConflictHarness(
	t *testing.T,
	bufferedTable string,
) *oidcRouterHarness {
	t.Helper()
	// Given: a paired agent owns a started remote deployment.
	h := newOIDCRouterHarness(t)
	seedAgentRouteUser(t, h, "admin", "delete-admin")
	environmentID := seedAssignableAgent(t, h)
	if _, err := h.repo.DB.ExecContext(t.Context(), `
	INSERT INTO projects(id,name) VALUES(1,'delete history');
	INSERT INTO releases(id,project_id,version,steps_json)
	VALUES(1,1,'1','[]');
	INSERT INTO deployments(id,release_id,environment_id,status,assigned_agent_id)
	VALUES(1,1,?,'running','agent-a');
	INSERT INTO remote_deployment_claims
	(deployment_id,agent_id,state,claim_token_hash,ciphertext,
	 claim_expires_at,last_heartbeat_at,started_at)
	VALUES(1,'agent-a','started',zeroblob(32),'fixture',200,100,100)`,
		environmentID); err != nil {
		t.Fatal(err)
	}
	if bufferedTable == "remote_step_runs" {
		if _, err := h.repo.DB.Exec(`INSERT INTO deployment_steps
		(deployment_id,step_index,name,script_body)
		VALUES(1,0,'terminal step','echo done');
		INSERT INTO remote_step_runs
		(deployment_id,step_index,agent_id,state,log_buffer_ciphertext)
		VALUES(1,0,'agent-a','succeeded','buffered-logs')`); err != nil {
			t.Fatal(err)
		}
	}
	if bufferedTable != "" {
		var buffer any
		if bufferedTable == "remote_deployment_claims" {
			buffer = "buffered-logs"
		}
		if _, err := h.repo.DB.Exec(`UPDATE remote_deployment_claims
		SET state='succeeded',finished_at=101,log_buffer_ciphertext=?
		WHERE deployment_id=1`, buffer); err != nil {
			t.Fatal(err)
		}
	}
	return h
}

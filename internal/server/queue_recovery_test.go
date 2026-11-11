package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"durpdeploy/internal/handler/api"
	"github.com/stretchr/testify/require"
)

func TestQueueRecoveryAPIE2E(t *testing.T) {
	h := newAgentDeleteConflictHarness(t, "")
	require.NoError(t, h.repo.ReconcileDeploymentQueues(t.Context()))
	srv := httptest.NewServer(
		NewRouter(h.repo, h.runner, h.parser, h.authHandler),
	)
	t.Cleanup(srv.Close)
	request := func(path, body string, status int) *http.Response {
		t.Helper()
		req, err := http.NewRequestWithContext(
			t.Context(),
			"POST",
			srv.URL+path,
			strings.NewReader(body),
		)
		require.NoError(t, err)
		req.Header.Set(
			"Authorization",
			"Bearer "+agentTestToken("delete-admin"),
		)
		req.Header.Set("Content-Type", "application/json")
		res, err := srv.Client().Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { res.Body.Close() })
		require.Equal(t, status, res.StatusCode)
		return res
	}
	path := "/api/v1/admin/maintenance/deployments/1/reconcile"
	request(path, `{"force":true}`, 400)
	request("/api/v1/admin/maintenance/deployments/-1/reconcile", "", 400)
	request("/api/v1/admin/maintenance/deployments/no/reconcile", "", 400)
	request(path, "{}", 409)
	request("/api/v1/admin/maintenance/deployments/999/reconcile", "", 404)
	var report api.QueueMaintenanceResponse
	require.NoError(
		t,
		json.NewDecoder(request("/api/v1/admin/maintenance", "", 200).Body).
			Decode(&report),
	)
	require.Len(t, report.Slots, 1)
	require.Contains(t, report.Slots[0].BlockerIDs, int64(1))
	require.Len(t, report.Slots[0].RemoteRuns, 1)
	_, err := h.repo.DB.ExecContext(
		t.Context(),
		`UPDATE deployments SET status='failed' WHERE id=1;
UPDATE remote_deployment_claims SET state='cleanup_unconfirmed',finished_at=2 WHERE deployment_id=1`,
	)
	require.NoError(t, err)
	request(path, "", 409)
	_, err = h.repo.DB.ExecContext(
		t.Context(),
		`UPDATE remote_deployment_claims SET cleanup_confirmed_at=1 WHERE deployment_id=1`,
	)
	require.NoError(t, err)
	_, err = h.repo.DB.ExecContext(
		t.Context(),
		`CREATE TRIGGER reject_queue_audit BEFORE INSERT ON audit_log
WHEN NEW.action='reconcile_deployment_queue' BEGIN SELECT RAISE(ABORT,'audit unavailable'); END`,
	)
	require.NoError(t, err)
	request(path, "{}", 500)
	owner, err := h.repo.Queries.GetDeploymentSlot(t.Context(), 1)
	require.NoError(t, err)
	require.Equal(t, int64(1), owner)
	_, err = h.repo.DB.ExecContext(
		t.Context(),
		"DROP TRIGGER reject_queue_audit",
	)
	require.NoError(t, err)
	for _, want := range []bool{true, false} {
		var result api.QueueReconciliationResponse
		require.NoError(
			t,
			json.NewDecoder(request(path, "{}", 200).Body).Decode(&result),
		)
		require.Equal(t, want, result.Changed)
	}
	var actor int64
	var details string
	require.NoError(
		t,
		h.repo.DB.QueryRowContext(t.Context(), `SELECT user_id,details FROM audit_log
WHERE action='reconcile_deployment_queue' AND entity_id=1`).
			Scan(&actor, &details),
	)
	require.Positive(t, actor)
	require.Contains(t, details, "api_token_id")
	assertAuditActionCount(t, h, "reconcile_deployment_queue", 1)
}

func TestQueueRecoveryRequiresAdministrator(t *testing.T) {
	for _, role := range []string{"", "viewer", "deployer"} {
		t.Run(role, func(t *testing.T) {
			h := newOIDCRouterHarness(t)
			if role != "" {
				seedAgentRouteUser(t, h, role, "recovery-denied")
			}
			for _, path := range []string{"/api/v1/admin/maintenance", "/api/v1/admin/maintenance/deployments/1/reconcile"} {
				req := httptest.NewRequest("POST", path, nil)
				req.Header.Set(
					"Authorization",
					"Bearer "+agentTestToken("recovery-denied"),
				)
				res := httptest.NewRecorder()
				NewRouter(
					h.repo,
					h.runner,
					h.parser,
					h.authHandler,
				).ServeHTTP(res, req)
				want := 403
				if role == "" {
					want = 401
				}
				require.Equal(t, want, res.Code)
			}
		})
	}
}

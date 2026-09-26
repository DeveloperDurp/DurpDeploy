package api_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
)

func TestEnvironmentDeleteRejectsUnconfirmedRemoteExecution(t *testing.T) {
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "development")
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "remote-delete@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	for _, statement := range []string{
		fmt.Sprintf(`INSERT INTO releases(project_id, version, steps_json)
VALUES(%d, 'remote', '[]')`, project.ID),
		fmt.Sprintf(`INSERT INTO deployments(release_id, environment_id, status)
VALUES((SELECT id FROM releases WHERE project_id=%d), %d, 'failed')`,
			project.ID, environment.ID),
		`INSERT INTO agents(id, name, endpoint)
VALUES('remote', 'Remote', 'https://remote.example')`,
		`INSERT INTO remote_deployment_claims(
deployment_id, agent_id, state, claim_token_hash, ciphertext,
claim_expires_at, last_heartbeat_at, started_at, finished_at,
cancel_requested_at)
VALUES((SELECT id FROM deployments WHERE status='failed'), 'remote',
'cancel_unconfirmed', zeroblob(32), 'encrypted', 1, 1, 1, 2, 1)`,
	} {
		if _, err := h.repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	req := httptest.NewRequest(http.MethodDelete,
		fmt.Sprintf("/api/v1/environments/%d", environment.ID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("delete status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int
	if err := h.repo.DB.QueryRow(`SELECT COUNT(*) FROM remote_deployment_claims`).
		Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("claim count=%d err=%v", count, err)
	}
}

package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/server"
)

func TestRunbookRetryRejectsUnconfirmedRemoteOutcome(t *testing.T) {
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "development")
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "runbook-retry@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	book, version, err := h.repo.SaveRunbook(context.Background(),
		repository.RunbookSave{
			ProjectID: project.ID, Name: "maintenance",
			StepsJSON: `[{"name":"check","script_body":"true"}]`,
		})
	if err != nil {
		t.Fatal(err)
	}
	execution, _, err := h.repo.CreateRunbookExecution(context.Background(),
		repository.RunbookExecutionRequest{
			ProjectID: project.ID, RunbookID: book.ID,
			VersionID: version.ID, EnvironmentID: environment.ID,
		})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		fmt.Sprintf(`UPDATE deployments SET status='failed'
WHERE id=%d`, execution.DeploymentID),
		`INSERT INTO agents(id, name, endpoint)
VALUES('remote', 'Remote', 'https://remote.example')`,
		fmt.Sprintf(`INSERT INTO remote_deployment_claims(
deployment_id, agent_id, state, claim_token_hash, ciphertext,
claim_expires_at, last_heartbeat_at, started_at, finished_at,
cancel_requested_at)
VALUES(%d, 'remote', 'cancel_unconfirmed', zeroblob(32),
'encrypted', 1, 1, 1, 2, 1)`, execution.DeploymentID),
	} {
		if _, err := h.repo.DB.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	req := httptest.NewRequest(http.MethodPost,
		fmt.Sprintf("/api/v1/projects/%d/runbook-executions/%d/retry",
			project.ID, execution.ID), nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("retry status=%d body=%s", rec.Code, rec.Body.String())
	}
	var count int
	if err := h.repo.DB.QueryRow(
		`SELECT COUNT(*) FROM runbook_executions WHERE runbook_version_id=?`,
		version.ID,
	).Scan(&count); err != nil || count != 1 {
		t.Fatalf("execution count=%d err=%v", count, err)
	}
}

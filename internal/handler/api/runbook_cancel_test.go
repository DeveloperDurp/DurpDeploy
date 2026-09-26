package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/server"
)

func TestRunbookAPI_CancelAcknowledgesRequestBeforeTerminalState(t *testing.T) {
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "development")
	h := newAPIHarness(t)
	t.Cleanup(h.runner.KillAll)
	user := seedAPIUser(t, h.repo, "runbook-cancel@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	book, _, err := h.repo.SaveRunbook(context.Background(),
		repository.RunbookSave{
			ProjectID: project.ID,
			Name:      "long-running",
			StepsJSON: `[{"name":"wait","script_body":"sleep 30","interpreter":"bash"}]`,
		})
	if err != nil {
		t.Fatal(err)
	}
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	created := request(http.MethodPost,
		fmt.Sprintf("%s/runbooks/%d/executions", base, book.ID),
		fmt.Sprintf(`{"environment_id":%d}`, environment.ID))
	if created.Code != http.StatusCreated {
		t.Fatalf("execute status=%d body=%s", created.Code,
			created.Body.String())
	}
	var execution db.RunbookExecution
	if err := json.Unmarshal(created.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		deployment, err := h.repo.Queries.GetDeployment(context.Background(),
			execution.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		if deployment.Status == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancelled := request(http.MethodPost,
		fmt.Sprintf("%s/runbook-executions/%d/cancel", base, execution.ID), "")
	if cancelled.Code != http.StatusOK ||
		!strings.Contains(
			cancelled.Body.String(),
			`"status":"cancellation_requested"`,
		) {
		t.Fatalf("cancel status=%d body=%s", cancelled.Code,
			cancelled.Body.String())
	}
	for time.Now().Before(deadline) {
		deployment, err := h.repo.Queries.GetDeployment(context.Background(),
			execution.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		if deployment.Status == "cancelled" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("runbook did not reach cancelled status")
}

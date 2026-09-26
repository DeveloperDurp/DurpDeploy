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
	"durpdeploy/internal/server"
)

func TestRunbookAPI_VersionedExecutionStaysSeparate(t *testing.T) {
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "development")
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "runbook-admin@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
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
	created := request(
		http.MethodPost,
		base+"/runbooks",
		`{"name":"maintenance","steps":[{"name":"check","script_body":"printf 'version one\\n'"}]}`,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf(
			"create status=%d body=%s",
			created.Code,
			created.Body.String(),
		)
	}
	var saved struct {
		Runbook struct {
			ID int64 `json:"id"`
		} `json:"runbook"`
		Version struct {
			ID int64 `json:"id"`
		} `json:"version"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	updated := request(http.MethodPut,
		fmt.Sprintf("%s/runbooks/%d", base, saved.Runbook.ID),
		`{"steps":[{"name":"check","script_body":"printf 'version two\\n'"}]}`)
	if updated.Code != http.StatusCreated {
		t.Fatalf(
			"update status=%d body=%s",
			updated.Code,
			updated.Body.String(),
		)
	}
	executed := request(http.MethodPost,
		fmt.Sprintf("%s/runbooks/%d/executions", base, saved.Runbook.ID),
		fmt.Sprintf(`{"environment_id":%d,"version_id":%d}`,
			environment.ID, saved.Version.ID))
	if executed.Code != http.StatusCreated {
		t.Fatalf(
			"execute status=%d body=%s",
			executed.Code,
			executed.Body.String(),
		)
	}
	var execution struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(executed.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var status string
	for time.Now().Before(deadline) {
		detail := request(http.MethodGet,
			fmt.Sprintf("%s/runbook-executions/%d", base, execution.ID), "")
		if detail.Code != http.StatusOK {
			t.Fatalf(
				"detail status=%d body=%s",
				detail.Code,
				detail.Body.String(),
			)
		}
		var state struct {
			Status      string `json:"status"`
			RunbookName string `json:"runbook_name"`
		}
		if err := json.Unmarshal(detail.Body.Bytes(), &state); err != nil {
			t.Fatal(err)
		}
		if state.RunbookName != "maintenance" {
			t.Fatalf("detail runbook_name=%q", state.RunbookName)
		}
		status = state.Status
		if status == "succeeded" || status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "succeeded" {
		logs := request(
			http.MethodGet,
			fmt.Sprintf(
				"%s/runbook-executions/%d/logs",
				base,
				execution.ID,
			),
			"",
		)
		t.Fatalf("execution status=%s logs=%s", status, logs.Body.String())
	}
	logs := request(http.MethodGet,
		fmt.Sprintf("%s/runbook-executions/%d/logs", base, execution.ID), "")
	if logs.Code != http.StatusOK ||
		!strings.Contains(logs.Body.String(), "version one") ||
		strings.Contains(logs.Body.String(), "version two") {
		t.Fatalf("logs status=%d body=%s", logs.Code, logs.Body.String())
	}
	for _, path := range []string{base + "/deployments", base + "/releases"} {
		list := request(http.MethodGet, path, "")
		if list.Code != http.StatusOK ||
			strings.Contains(list.Body.String(), "runbook:") ||
			strings.Contains(list.Body.String(), "version one") {
			t.Fatalf(
				"ordinary history path=%s status=%d body=%s",
				path,
				list.Code,
				list.Body.String(),
			)
		}
	}
	unusedEnvironment := seedEnv(t, h.repo)
	if _, err := h.repo.Queries.CreateRunbookSchedule(context.Background(),
		db.CreateRunbookScheduleParams{
			RunbookID:     saved.Runbook.ID,
			EnvironmentID: unusedEnvironment.ID,
			Cron:          "0 3 * * *", NextRunAt: time.Now().Unix() + 86400,
		}); err != nil {
		t.Fatal(err)
	}
	deletedEnvironment := request(http.MethodDelete,
		fmt.Sprintf("/api/v1/environments/%d", unusedEnvironment.ID), "")
	if deletedEnvironment.Code != http.StatusNoContent {
		t.Fatalf("delete scheduled environment status=%d body=%s",
			deletedEnvironment.Code, deletedEnvironment.Body.String())
	}
	deletedEnvironment = request(http.MethodDelete,
		fmt.Sprintf("/api/v1/environments/%d", environment.ID), "")
	if deletedEnvironment.Code != http.StatusNoContent {
		t.Fatalf("delete executed environment status=%d body=%s",
			deletedEnvironment.Code, deletedEnvironment.Body.String())
	}
}

func TestRunbookAPI_ProjectDeleteAfterVersion(t *testing.T) {
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "development")
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "runbook-delete@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	request := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	created := request(
		http.MethodPost,
		base+"/runbooks",
		`{"name":"maintenance","steps":[{"name":"check","script_body":"true"}]}`,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf(
			"create status=%d body=%s",
			created.Code,
			created.Body.String(),
		)
	}
	var saved struct {
		Runbook struct {
			ID int64 `json:"id"`
		} `json:"runbook"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &saved); err != nil {
		t.Fatal(err)
	}
	executed := request(http.MethodPost,
		fmt.Sprintf("%s/runbooks/%d/executions", base, saved.Runbook.ID),
		fmt.Sprintf(`{"environment_id":%d}`, environment.ID))
	if executed.Code != http.StatusCreated {
		t.Fatalf("execute status=%d body=%s", executed.Code,
			executed.Body.String())
	}
	var execution struct {
		DeploymentID int64 `json:"deployment_id"`
	}
	if err := json.Unmarshal(executed.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		deployment, err := h.repo.Queries.GetDeployment(context.Background(),
			execution.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		if deployment.Status == "succeeded" || deployment.Status == "failed" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	updated := request(http.MethodPut,
		fmt.Sprintf("%s/runbooks/%d", base, saved.Runbook.ID),
		`{"steps":[{"name":"wait","script_body":"exec sleep 10"}]}`)
	if updated.Code != http.StatusCreated {
		t.Fatalf("update status=%d body=%s", updated.Code,
			updated.Body.String())
	}
	active := request(http.MethodPost,
		fmt.Sprintf("%s/runbooks/%d/executions", base, saved.Runbook.ID),
		fmt.Sprintf(`{"environment_id":%d}`, environment.ID))
	if active.Code != http.StatusCreated {
		t.Fatalf("active execution status=%d body=%s", active.Code,
			active.Body.String())
	}
	var running struct {
		ID           int64 `json:"id"`
		DeploymentID int64 `json:"deployment_id"`
	}
	if err := json.Unmarshal(active.Body.Bytes(), &running); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	status := ""
	for time.Now().Before(deadline) {
		deployment, err := h.repo.Queries.GetDeployment(context.Background(),
			running.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		status = deployment.Status
		if status == "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "running" {
		t.Fatalf("execution did not start: %s", status)
	}
	blockedEnvironment := request(http.MethodDelete,
		fmt.Sprintf("/api/v1/environments/%d", environment.ID), "")
	if blockedEnvironment.Code != http.StatusConflict {
		t.Fatalf("active environment delete status=%d body=%s",
			blockedEnvironment.Code, blockedEnvironment.Body.String())
	}
	blocked := request(http.MethodDelete, base, "")
	if blocked.Code != http.StatusConflict {
		t.Fatalf("active delete status=%d body=%s", blocked.Code,
			blocked.Body.String())
	}
	cancelled := request(http.MethodPost,
		fmt.Sprintf("%s/runbook-executions/%d/cancel", base, running.ID), "")
	if cancelled.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", cancelled.Code,
			cancelled.Body.String())
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		deployment, err := h.repo.Queries.GetDeployment(context.Background(),
			running.DeploymentID)
		if err != nil {
			t.Fatal(err)
		}
		status = deployment.Status
		if status == "cancelled" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if status != "cancelled" {
		t.Fatalf("execution did not cancel: %s", status)
	}
	deleted := request(http.MethodDelete, base, "")
	if deleted.Code != http.StatusNoContent {
		t.Fatalf(
			"delete status=%d body=%s",
			deleted.Code,
			deleted.Body.String(),
		)
	}
}

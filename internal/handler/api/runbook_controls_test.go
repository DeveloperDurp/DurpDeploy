package api_test

import (
	"context"
	"database/sql"
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

func TestRunbookAPI_ApprovalAndSchedule(t *testing.T) {
	t.Setenv("DURPDEPLOY_EXECUTION_BOUNDARY", "development")
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "runbook-controls@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	laterEnvironment := seedEnv(t, h.repo)
	ctx := context.Background()
	lifecycle, err := h.repo.Queries.CreateLifecycle(ctx,
		db.CreateLifecycleParams{Name: "protected"})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.repo.Queries.SetProjectLifecycle(ctx,
		db.SetProjectLifecycleParams{
			ID: project.ID,
			LifecycleID: sql.NullInt64{
				Int64: lifecycle.ID, Valid: true,
			},
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.Queries.CreateLifecycleStage(ctx,
		db.CreateLifecycleStageParams{
			LifecycleID: lifecycle.ID, EnvironmentID: environment.ID,
			RequiresApproval: 1,
		}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.Queries.CreateLifecycleStage(ctx,
		db.CreateLifecycleStageParams{
			LifecycleID: lifecycle.ID, EnvironmentID: laterEnvironment.ID,
			SortOrder: 1,
		}); err != nil {
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
	created := request(
		http.MethodPost,
		base+"/runbooks",
		`{"name":"maintenance","steps":[{"name":"check","script_body":"printf 'approved\\n'"}]}`,
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
	bookURL := fmt.Sprintf("%s/runbooks/%d", base, saved.Runbook.ID)
	blocked := request(http.MethodPost, bookURL+"/executions",
		fmt.Sprintf(`{"environment_id":%d}`, laterEnvironment.ID))
	if blocked.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ungated later stage status=%d body=%s", blocked.Code,
			blocked.Body.String())
	}
	executed := request(http.MethodPost, bookURL+"/executions",
		fmt.Sprintf(`{"environment_id":%d}`, environment.ID))
	if executed.Code != http.StatusCreated {
		t.Fatalf("execute status=%d body=%s", executed.Code,
			executed.Body.String())
	}
	var execution struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(executed.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	executionURL := fmt.Sprintf("%s/runbook-executions/%d", base,
		execution.ID)
	before := request(http.MethodGet, executionURL, "")
	if before.Code != http.StatusOK ||
		!strings.Contains(before.Body.String(), `"status":"pending_approval"`) {
		t.Fatalf("before approval status=%d body=%s", before.Code,
			before.Body.String())
	}
	deletion := request(http.MethodDelete, base, "")
	if deletion.Code != http.StatusConflict {
		t.Fatalf("delete pending approval status=%d body=%s",
			deletion.Code, deletion.Body.String())
	}
	approved := request(http.MethodPost, executionURL+"/approve", "")
	if approved.Code != http.StatusOK {
		t.Fatalf("approve status=%d body=%s", approved.Code,
			approved.Body.String())
	}
	deadline := time.Now().Add(5 * time.Second)
	succeeded := false
	for time.Now().Before(deadline) {
		detail := request(http.MethodGet, executionURL, "")
		if strings.Contains(detail.Body.String(), `"status":"succeeded"`) {
			succeeded = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !succeeded {
		t.Fatalf("approved execution did not succeed")
	}
	late := request(http.MethodPost, bookURL+"/executions",
		fmt.Sprintf(`{"environment_id":%d}`, laterEnvironment.ID))
	if late.Code != http.StatusCreated {
		t.Fatalf("promoted stage status=%d body=%s", late.Code,
			late.Body.String())
	}

	logs := request(http.MethodGet, executionURL+"/logs", "")
	if !strings.Contains(logs.Body.String(), "approved") {
		t.Fatalf("approved execution logs=%s", logs.Body.String())
	}
	scheduled := request(http.MethodPost, bookURL+"/schedules",
		fmt.Sprintf(`{"environment_id":%d,"version_id":%d,"cron":"0 3 * * *"}`,
			environment.ID, saved.Version.ID))
	if scheduled.Code != http.StatusCreated {
		t.Fatalf("schedule status=%d body=%s", scheduled.Code,
			scheduled.Body.String())
	}
	var schedule struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(scheduled.Body.Bytes(), &schedule); err != nil {
		t.Fatal(err)
	}
	listed := request(http.MethodGet, bookURL+"/schedules", "")
	if listed.Code != http.StatusOK ||
		!strings.Contains(
			listed.Body.String(),
			fmt.Sprintf(`"id":%d`, schedule.ID),
		) {
		t.Fatalf("schedules status=%d body=%s", listed.Code,
			listed.Body.String())
	}
	disabled := request(http.MethodPost,
		fmt.Sprintf("%s/schedules/%d/disable", bookURL, schedule.ID), "")
	if disabled.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", disabled.Code,
			disabled.Body.String())
	}
}

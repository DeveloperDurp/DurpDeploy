package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/server"
)

func TestRunbookAPI_ExecutesWithDockerRuntime(t *testing.T) {
	h := newAPIHarness(t, fakeDocker(t))
	user := seedAPIUser(t, h.repo, "docker-runtime@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	request := func(method, path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	base := fmt.Sprintf("/api/v1/projects/%d", project.ID)
	created := request(http.MethodPost, base+"/runbooks",
		`{"name":"docker","steps":[{"name":"check",`+
			`"script_body":"printf 'docker runtime\\n'",`+
			`"container_image":"alpine:3.20"}]}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body)
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
		t.Fatalf("execute status=%d body=%s", executed.Code, executed.Body)
	}
	var execution struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(executed.Body.Bytes(), &execution); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		detail := request(http.MethodGet,
			fmt.Sprintf("%s/runbook-executions/%d", base, execution.ID), "")
		if strings.Contains(detail.Body.String(), `"status":"succeeded"`) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("Docker-backed execution did not succeed")
}

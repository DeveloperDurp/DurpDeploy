package api_test

import (
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

type legacyAPIClient struct {
	t     *testing.T
	h     *harness
	token string
}

func (c legacyAPIClient) request(
	method, path, body string,
) *httptest.ResponseRecorder {
	c.t.Helper()
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	router := server.NewRouter(
		c.h.repo,
		c.h.runner,
		parser,
		handler.NewAuthHandler(c.h.repo),
	)
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestAPIRefreshRejectsLegacySnapshotAfterStepsChange(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "refresh-boundary@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	client := legacyAPIClient{t, h, token}
	project := seedProject(t, h.repo)
	legacy := `[{"name":"old","script_body":"true"}]`
	release, err := h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "old", StepsJson: legacy,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.CreateStepWithPlacement(
		t.Context(),
		db.CreateStepParams{
			ProjectID:      project.ID,
			Name:           "new",
			ScriptBody:     "true",
			ContainerImage: "alpine:3.20",
		},
		"local",
		nil,
	); err != nil {
		t.Fatal(err)
	}

	response := client.request(
		http.MethodPost,
		fmt.Sprintf(
			"/api/v1/projects/%d/releases/%d/refresh",
			project.ID,
			release.ID,
		),
		"",
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(
			response.Body.String(),
			repository.ErrLegacyServerStep.Error(),
		) {
		t.Fatalf("refresh: %d %s", response.Code, response.Body.String())
	}
	stored, err := h.repo.Queries.GetRelease(t.Context(), release.ID)
	if err != nil || stored.StepsJson != legacy {
		t.Fatalf("historical snapshot changed: %+v %v", stored, err)
	}
	created := client.request(
		http.MethodPost,
		fmt.Sprintf(
			"/api/v1/projects/%d/releases",
			project.ID,
		),
		`{"version":"new"}`,
	)
	if created.Code != http.StatusCreated {
		t.Fatalf("new release: %d %s", created.Code, created.Body.String())
	}
	var fresh db.Release
	mustDecode(t, created.Body, &fresh)
	if err := h.repo.ValidateExecutableRelease(
		t.Context(),
		fresh.ID,
	); err != nil {
		t.Fatalf("new release cannot execute: %v", err)
	}
}

func TestAPISchedulesRejectLegacyReleaseBeforeWrites(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "schedule-boundary@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	client := legacyAPIClient{t, h, token}
	project := seedProject(t, h.repo)
	env := seedEnv(t, h.repo)
	legacy, err := h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID,
			Version:   "old",
			StepsJson: `[{"name":"old","script_body":"true"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	valid := seedRelease(t, h.repo, project.ID)
	base := fmt.Sprintf("/api/v1/projects/%d/schedules", project.ID)
	body := fmt.Sprintf(
		`{"release_id":%d,"environment_id":%d,"cron":"0 0 * * *","enabled":true}`,
		legacy.ID,
		env.ID,
	)
	response := client.request(http.MethodPost, base, body)
	if response.Code != http.StatusConflict ||
		!strings.Contains(
			response.Body.String(),
			repository.ErrLegacyServerStep.Error(),
		) {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	rows, err := h.repo.Queries.ListScheduledDeploymentsByProject(
		t.Context(),
		project.ID,
	)
	if err != nil || len(rows) != 0 {
		t.Fatalf("create mutated schedules: %+v %v", rows, err)
	}
	schedule, err := h.repo.Queries.CreateScheduledDeployment(
		t.Context(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     project.ID,
			ReleaseID:     valid.ID,
			EnvironmentID: env.ID,
			Cron:          "0 0 * * *",
			NextRunAt:     time.Now().Add(time.Hour).Unix(),
			Enabled:       0,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("%s/%d", base, schedule.ID)
	response = client.request(http.MethodPut, path, body)
	if response.Code != http.StatusConflict {
		t.Fatalf("update: %d %s", response.Code, response.Body.String())
	}
	stored, err := h.repo.Queries.GetScheduledDeployment(
		t.Context(),
		schedule.ID,
	)
	if err != nil || stored.ReleaseID != valid.ID || stored.Enabled != 0 {
		t.Fatalf("update mutated schedule: %+v %v", stored, err)
	}
	legacySchedule, err := h.repo.Queries.CreateScheduledDeployment(
		t.Context(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     project.ID,
			ReleaseID:     legacy.ID,
			EnvironmentID: env.ID,
			Cron:          "0 0 * * *",
			NextRunAt:     time.Now().Add(time.Hour).Unix(),
			Enabled:       0,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response = client.request(http.MethodPost,
		fmt.Sprintf("%s/%d/toggle", base, legacySchedule.ID), "")
	if response.Code != http.StatusConflict ||
		!strings.Contains(
			response.Body.String(),
			repository.ErrLegacyServerStep.Error(),
		) {
		t.Fatalf("enable: %d %s", response.Code, response.Body.String())
	}
	stored, err = h.repo.Queries.GetScheduledDeployment(
		t.Context(),
		legacySchedule.ID,
	)
	if err != nil || stored.Enabled != 0 ||
		stored.NextRunAt != legacySchedule.NextRunAt {
		t.Fatalf("enable mutated schedule: %+v %v", stored, err)
	}
}

func TestAPIRunbookScheduleRejectsPinnedLegacyVersion(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "book-boundary@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	client := legacyAPIClient{t, h, token}
	project := seedProject(t, h.repo)
	env := seedEnv(t, h.repo)
	book, version, err := h.repo.SaveRunbook(
		t.Context(),
		repository.RunbookSave{
			ProjectID: project.ID, Name: "old-book",
			StepsJSON: `[{"name":"old","script_body":"true"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response := client.request(
		http.MethodPost,
		fmt.Sprintf(
			"/api/v1/projects/%d/runbooks/%d/schedules",
			project.ID,
			book.ID,
		),
		fmt.Sprintf(
			`{"environment_id":%d,"version_id":%d,"cron":"0 0 * * *"}`,
			env.ID,
			version.ID,
		),
	)
	if response.Code != http.StatusConflict ||
		!strings.Contains(
			response.Body.String(),
			repository.ErrLegacyServerStep.Error(),
		) {
		t.Fatalf(
			"pinned schedule: %d %s",
			response.Code,
			response.Body.String(),
		)
	}
	rows, err := h.repo.Queries.ListRunbookSchedules(t.Context(), book.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("pinned schedule mutated DB: %+v %v", rows, err)
	}
}

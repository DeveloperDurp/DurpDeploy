package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/scheduler"
	"durpdeploy/internal/server"

	"github.com/robfig/cron/v3"
)

func TestScheduleAPI_ExposesLegacyFailureAfterSchedulerDisables(t *testing.T) {
	h := newAPIHarness(t)
	user := seedAPIUser(t, h.repo, "schedule-admin@example.com", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	environment := seedEnv(t, h.repo)
	release, err := h.repo.Queries.CreateRelease(
		t.Context(),
		db.CreateReleaseParams{
			ProjectID: project.ID, Version: "legacy-scheduled",
			StepsJson: `[{"name":"legacy","script_body":"true"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	deploySchedule, err := h.repo.Queries.CreateScheduledDeployment(t.Context(),
		db.CreateScheduledDeploymentParams{
			ProjectID: project.ID, ReleaseID: release.ID,
			EnvironmentID: environment.ID, Cron: "* * * * *",
			NextRunAt: time.Now().Add(-time.Minute).Unix(), Enabled: 1,
		})
	if err != nil {
		t.Fatal(err)
	}
	book, _, err := h.repo.SaveRunbook(t.Context(), repository.RunbookSave{
		ProjectID: project.ID, Name: "legacy-book",
		StepsJSON: `[{"name":"legacy","script_body":"true"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = h.repo.Queries.CreateRunbookSchedule(t.Context(),
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, EnvironmentID: environment.ID,
			Cron: "* * * * *", NextRunAt: time.Now().Add(-time.Minute).Unix(),
		})
	if err != nil {
		t.Fatal(err)
	}

	scheduler.New(h.repo, h.runner).Tick(t.Context())

	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	router := server.NewRouter(h.repo, h.runner, parser,
		handler.NewAuthHandler(h.repo))
	type scheduleState struct {
		Enabled   int64  `json:"enabled"`
		LastError string `json:"last_error"`
	}
	for _, test := range []struct {
		path string
		list bool
	}{
		{fmt.Sprintf("/api/v1/projects/%d/schedules/%d", project.ID, deploySchedule.ID), false},
		{fmt.Sprintf("/api/v1/projects/%d/runbooks/%d/schedules", project.ID, book.ID), true},
	} {
		t.Run(test.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusOK {
				t.Fatalf(
					"GET %s: %d %s",
					test.path,
					response.Code,
					response.Body.String(),
				)
			}
			var row scheduleState
			if test.list {
				var items []scheduleState
				if err := json.Unmarshal(
					response.Body.Bytes(),
					&items,
				); err != nil {
					t.Fatal(err)
				}
				if len(items) != 1 {
					t.Fatalf(
						"GET %s: schedules=%d want 1",
						test.path,
						len(items),
					)
				}
				row = items[0]
			} else {
				if err := json.Unmarshal(
					response.Body.Bytes(),
					&row,
				); err != nil {
					t.Fatal(err)
				}
			}
			if row.Enabled != 0 ||
				!strings.Contains(row.LastError,
					"recreate the step and create a new release") {
				t.Fatalf(
					"GET %s: no actionable disabled state: %+v",
					test.path,
					row,
				)
			}
		})
	}
}

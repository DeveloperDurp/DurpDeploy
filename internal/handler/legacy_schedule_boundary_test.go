package handler_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestWebRefreshRejectsHistoricalReleaseAfterStepRecreation(t *testing.T) {
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "old-refresh"},
	)
	if err != nil {
		t.Fatal(err)
	}
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
	response, err := h.authedClient().PostForm(fmt.Sprintf(
		"%s/projects/%d/releases/%d/refresh",
		h.server.URL,
		project.ID,
		release.ID,
	),
		url.Values{"csrf_token": {h.csrfToken()}})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict ||
		!strings.Contains(
			string(body),
			repository.ErrLegacyServerStep.Error(),
		) {
		t.Fatalf("refresh: %d %s", response.StatusCode, body)
	}
	stored, err := h.repo.Queries.GetRelease(t.Context(), release.ID)
	if err != nil || stored.StepsJson != legacy {
		t.Fatalf("historical snapshot changed: %+v %v", stored, err)
	}
}

func TestWebScheduleEnableRejectsLegacyRelease(t *testing.T) {
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "old-schedule"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := h.repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "old-schedule-env"},
	)
	if err != nil {
		t.Fatal(err)
	}
	release, err := h.repo.Queries.CreateRelease(
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
	schedule, err := h.repo.Queries.CreateScheduledDeployment(
		t.Context(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     project.ID,
			ReleaseID:     release.ID,
			EnvironmentID: env.ID,
			Cron:          "0 0 * * *",
			NextRunAt:     time.Now().Add(time.Hour).Unix(),
			Enabled:       0,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.authedClient().PostForm(fmt.Sprintf(
		"%s/projects/%d/schedules/%d/toggle",
		h.server.URL,
		project.ID,
		schedule.ID,
	),
		url.Values{"csrf_token": {h.csrfToken()}})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusConflict ||
		!strings.Contains(
			string(body),
			repository.ErrLegacyServerStep.Error(),
		) {
		t.Fatalf("enable: %d %s", response.StatusCode, body)
	}
	stored, err := h.repo.Queries.GetScheduledDeployment(
		t.Context(),
		schedule.ID,
	)
	if err != nil || stored.Enabled != 0 ||
		stored.NextRunAt != schedule.NextRunAt {
		t.Fatalf("enable mutated schedule: %+v %v", stored, err)
	}
}

func TestWebRunbookScheduleRejectsPinnedLegacyVersion(t *testing.T) {
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "old-book-schedule"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := h.repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "old-book-env"},
	)
	if err != nil {
		t.Fatal(err)
	}
	book, version, err := h.repo.SaveRunbook(
		t.Context(),
		repository.RunbookSave{
			ProjectID: project.ID,
			Name:      "old-book",
			StepsJSON: `[{"name":"old","script_body":"true"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	response, err := h.authedClient().PostForm(fmt.Sprintf(
		"%s/projects/%d/runbooks/%d/schedules",
		h.server.URL,
		project.ID,
		book.ID,
	),
		url.Values{
			"csrf_token": {
				h.csrfToken(),
			},
			"environment_id": {fmt.Sprint(env.ID)},
			"version_id":     {fmt.Sprint(version.ID)},
			"cron":           {"0 0 * * *"},
		})
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusConflict {
		t.Fatalf("pinned schedule: %d", response.StatusCode)
	}
	rows, err := h.repo.Queries.ListRunbookSchedules(t.Context(), book.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("pinned schedule mutated DB: %+v %v", rows, err)
	}
}

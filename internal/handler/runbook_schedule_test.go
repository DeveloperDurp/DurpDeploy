package handler_test

import (
	"context"
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

func TestRunbookWeb_RejectsInvalidScheduleCron(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project, err := h.repo.Queries.CreateProject(ctx,
		db.CreateProjectParams{Name: "runbook-cron"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := h.repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "runbook-cron-env"})
	if err != nil {
		t.Fatal(err)
	}
	book, _, err := h.repo.SaveRunbook(ctx, repository.RunbookSave{
		ProjectID: project.ID, Name: "maintenance",
		StepsJSON: `[{"name":"check","script_body":"true"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := fmt.Sprintf("%s/projects/%d/runbooks/%d/schedules",
		h.server.URL, project.ID, book.ID)
	for _, expr := range []string{
		"TZ=America/Chicago 0 3 * * *",
		"0 0 31 2 *",
	} {
		response, err := h.authedClient().PostForm(endpoint, url.Values{
			"csrf_token":     {h.csrfToken()},
			"environment_id": {fmt.Sprint(environment.ID)},
			"version_id":     {"0"},
			"cron":           {expr},
		})
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("cron %q status=%d", expr, response.StatusCode)
		}
	}
	schedules, err := h.repo.Queries.ListRunbookSchedules(ctx, book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(schedules) != 0 {
		t.Fatalf("invalid cron created %d schedules", len(schedules))
	}
}

// TestRunbookDetail_rendersScheduleLastErrorWhenDisabled covers the regression
// surfaced by the runbook scheduler auto-disabling with a recreate-step
// reason. Before the templ change, neither the desktop table nor the mobile
// card rendered the persisted last_error, so this test fails on the previous
// rendered HTML and passes once the page surfaces the reason.
func TestRunbookDetail_rendersScheduleLastErrorWhenDisabled(t *testing.T) {
	// Given
	h := newHarness(t)
	ctx := context.Background()
	project, err := h.repo.Queries.CreateProject(ctx,
		db.CreateProjectParams{Name: "runbook-last-error"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := h.repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "runbook-last-error-env"})
	if err != nil {
		t.Fatal(err)
	}
	book, _, err := h.repo.SaveRunbook(ctx, repository.RunbookSave{
		ProjectID: project.ID, Name: "maintenance",
		StepsJSON: `[{"name":"check","script_body":"true"}]`,
	})
	if err != nil {
		t.Fatal(err)
	}
	schedule, err := h.repo.Queries.CreateRunbookSchedule(ctx,
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, EnvironmentID: environment.ID,
			Cron:      "0 3 * * *",
			NextRunAt: time.Now().Add(time.Hour).Unix(),
		})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	const reason = "step foo no longer exists; recreate the book version"
	if _, err := h.repo.Queries.DisableRunbookScheduleWithReason(ctx,
		db.DisableRunbookScheduleWithReasonParams{
			LastError: reason, ID: schedule.ID,
			NextRunAt: schedule.NextRunAt,
		}); err != nil {
		t.Fatalf("disable with reason: %v", err)
	}

	// When
	response, err := h.authedClient().Get(fmt.Sprintf(
		"%s/projects/%d/runbooks/%d", h.server.URL, project.ID, book.ID,
	))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}

	// Then
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "Disabled — "+reason) {
		t.Fatalf("runbook detail omitted last_error %q; body=%s",
			reason, body)
	}
	if !strings.Contains(string(body), "data-runbook-schedule-last-error") {
		t.Fatalf("runbook detail missing runbook-schedule-last-error marker")
	}
}

package handler_test

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

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

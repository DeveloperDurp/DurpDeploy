package handler_test

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestLegacyLaunchWeb_RejectsImagelessRelease(t *testing.T) {
	for _, action := range []string{
		"deploy", "redeploy", "runbook execute", "runbook retry",
	} {
		t.Run(action, func(t *testing.T) {
			h := newHarness(t)
			project, err := h.repo.Queries.CreateProject(t.Context(),
				db.CreateProjectParams{Name: "legacy web"})
			if err != nil {
				t.Fatal(err)
			}
			environment, err := h.repo.Queries.CreateEnvironment(t.Context(),
				db.CreateEnvironmentParams{Name: "legacy env"})
			if err != nil {
				t.Fatal(err)
			}
			const steps = `[{"name":"old","script_body":"echo old","execution_target":"local"}]`
			form := url.Values{"csrf_token": {h.csrfToken()}}
			var path string
			if strings.HasPrefix(action, "runbook") {
				book, version, err := h.repo.SaveRunbook(t.Context(),
					repository.RunbookSave{
						ProjectID: project.ID,
						Name:      "old runbook",
						StepsJSON: steps,
					})
				if err != nil {
					t.Fatal(err)
				}
				path = fmt.Sprintf("/projects/%d/runbooks/%d/execute",
					project.ID, book.ID)
				form.Set("environment_id", fmt.Sprint(environment.ID))
				form.Set("version_id", fmt.Sprint(version.ID))
				if action == "runbook retry" {
					source, err := h.repo.Queries.CreateDeployment(t.Context(),
						db.CreateDeploymentParams{
							ReleaseID:     version.ReleaseID,
							EnvironmentID: environment.ID,
							Status:        "failed",
						})
					if err != nil {
						t.Fatal(err)
					}
					execution, err := h.repo.Queries.CreateRunbookExecution(
						t.Context(), db.CreateRunbookExecutionParams{
							RunbookVersionID: version.ID,
							DeploymentID:     source.ID,
						})
					if err != nil {
						t.Fatal(err)
					}
					path = fmt.Sprintf(
						"/projects/%d/runbooks/executions/%d/retry",
						project.ID,
						execution.ID,
					)
				}
			} else {
				release, err := h.repo.Queries.CreateRelease(t.Context(),
					db.CreateReleaseParams{
						ProjectID: project.ID, Version: "old", StepsJson: steps,
					})
				if err != nil {
					t.Fatal(err)
				}
				path = fmt.Sprintf("/projects/%d/deploy", project.ID)
				form.Set("release_id", fmt.Sprint(release.ID))
				form.Set("environment_id", fmt.Sprint(environment.ID))
				if action == "redeploy" {
					source, err := h.repo.Queries.CreateDeployment(t.Context(),
						db.CreateDeploymentParams{
							ReleaseID:     release.ID,
							EnvironmentID: environment.ID,
							Status:        "failed",
						})
					if err != nil {
						t.Fatal(err)
					}
					path = fmt.Sprintf("/deployments/%d/redeploy", source.ID)
				}
			}

			response, err := h.authedClient().PostForm(h.server.URL+path, form)
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
					"recreate the step and create a new release",
				) {
				t.Fatalf("status=%d body=%s", response.StatusCode, body)
			}
			var count int
			if err := h.repo.DB.QueryRowContext(t.Context(),
				"SELECT count(*) FROM deployments").Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if action == "redeploy" || action == "runbook retry" {
				want = 1
			}
			if count != want {
				t.Fatalf(
					"created deployment despite refusal: count=%d want=%d",
					count,
					want,
				)
			}
		})
	}
}

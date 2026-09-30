package api_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/server"
)

func TestLegacyLaunchAPI_RejectsImagelessRelease(t *testing.T) {
	for _, action := range []string{
		"deploy", "redeploy", "retry", "runbook execute", "runbook retry",
	} {
		t.Run(action, func(t *testing.T) {
			h := newAPIHarness(t)
			user := seedAPIUser(t, h.repo, "legacy-api@example.com", "admin")
			_, token := seedAPIToken(t, h.repo, user.ID)
			project := seedProject(t, h.repo)
			environment := seedEnv(t, h.repo)
			const steps = `[{"name":"old","script_body":"echo old","execution_target":"local"}]`
			var path, body string
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
				path = fmt.Sprintf("/api/v1/projects/%d/runbooks/%d/executions",
					project.ID, book.ID)
				body = fmt.Sprintf(`{"environment_id":%d,"version_id":%d}`,
					environment.ID, version.ID)
				if action == "runbook retry" {
					source := seedDeployment(t, h.repo, version.ReleaseID,
						environment.ID, "failed")
					execution, err := h.repo.Queries.CreateRunbookExecution(
						t.Context(), db.CreateRunbookExecutionParams{
							RunbookVersionID: version.ID,
							DeploymentID:     source.ID,
						})
					if err != nil {
						t.Fatal(err)
					}
					path = fmt.Sprintf(
						"/api/v1/projects/%d/runbook-executions/%d/retry",
						project.ID,
						execution.ID,
					)
					body = ""
				}
			} else {
				release, err := h.repo.Queries.CreateRelease(t.Context(),
					db.CreateReleaseParams{
						ProjectID: project.ID, Version: "old", StepsJson: steps,
					})
				if err != nil {
					t.Fatal(err)
				}
				path = fmt.Sprintf(
					"/api/v1/projects/%d/deployments",
					project.ID,
				)
				body = fmt.Sprintf(`{"release_id":%d,"environment_id":%d}`,
					release.ID, environment.ID)
				if action != "deploy" {
					source := seedDeployment(t, h.repo, release.ID,
						environment.ID, "failed")
					path = fmt.Sprintf(
						"/api/v1/deployments/%d/%s",
						source.ID,
						action,
					)
					body = ""
				}
			}
			router := server.NewRouter(
				h.repo,
				h.runner,
				cron.NewParser(
					cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow,
				),
				handler.NewAuthHandler(h.repo),
			)
			req := httptest.NewRequest(
				http.MethodPost,
				path,
				strings.NewReader(body),
			)
			req.Header.Set("Authorization", "Bearer "+token)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()

			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusConflict {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			var response struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(
				response.Error,
				"recreate the step and create a new release",
			) {
				t.Fatalf("missing recovery guidance: %q", response.Error)
			}
			var count int
			if err := h.repo.DB.QueryRowContext(context.Background(),
				"SELECT count(*) FROM deployments").Scan(&count); err != nil {
				t.Fatal(err)
			}
			want := 0
			if action == "redeploy" || action == "retry" ||
				action == "runbook retry" {
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

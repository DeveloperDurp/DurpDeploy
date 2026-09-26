package handler_test

import (
	"context"
	"fmt"
	"io"
	"strings"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestRunbookWeb_ReplaysExistingLogsOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	project, err := h.repo.Queries.CreateProject(ctx,
		db.CreateProjectParams{Name: "runbook-log-page"})
	if err != nil {
		t.Fatal(err)
	}
	environment, err := h.repo.Queries.CreateEnvironment(ctx,
		db.CreateEnvironmentParams{Name: "runbook-log-env"})
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
	execution, deployment, err := h.repo.CreateRunbookExecution(ctx,
		repository.RunbookExecutionRequest{
			ProjectID: project.ID, RunbookID: book.ID,
			EnvironmentID: environment.ID,
		})
	if err != nil {
		t.Fatal(err)
	}
	const line = "STREAM_REPLAY_ONLY_123"
	if _, err := h.repo.Queries.CreateDeploymentLog(ctx,
		db.CreateDeploymentLogParams{
			DeploymentID: deployment.Deployment.ID, Line: line,
		}); err != nil {
		t.Fatal(err)
	}
	pageURL := fmt.Sprintf("%s/projects/%d/runbooks/executions/%d",
		h.server.URL, project.ID, execution.ID)
	checkPage := func(status string, wantLine bool) {
		t.Helper()
		if err := h.repo.Queries.UpdateDeploymentStatus(ctx,
			db.UpdateDeploymentStatusParams{
				ID: deployment.Deployment.ID, Status: status,
			}); err != nil {
			t.Fatal(err)
		}
		response, err := h.authedClient().Get(pageURL)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), line) != wantLine {
			t.Fatalf("status=%s rendered log=%t, want %t", status,
				strings.Contains(string(body), line), wantLine)
		}
	}
	checkPage("running", false)
	checkPage("succeeded", true)
}

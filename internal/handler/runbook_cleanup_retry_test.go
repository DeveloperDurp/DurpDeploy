package handler_test

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func TestWebRunbookRetryRejectsUnconfirmedCleanup(t *testing.T) {
	// Given
	h := newHarness(t)
	project, err := h.repo.Queries.CreateProject(
		t.Context(),
		db.CreateProjectParams{Name: "p"},
	)
	if err != nil {
		t.Fatal(err)
	}
	env, err := h.repo.Queries.CreateEnvironment(
		t.Context(),
		db.CreateEnvironmentParams{Name: "e"},
	)
	if err != nil {
		t.Fatal(err)
	}
	book, version, err := h.repo.SaveRunbook(
		t.Context(),
		repository.RunbookSave{
			ProjectID: project.ID, Name: "maintenance",
			StepsJSON: `[{"name":"local","container_image":"alpine:3.20"}]`,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	execution, _, err := h.repo.CreateRunbookExecution(
		t.Context(),
		repository.RunbookExecutionRequest{
			ProjectID: project.ID, RunbookID: book.ID,
			VersionID: version.ID, EnvironmentID: env.ID,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.repo.DB.ExecContext(
		t.Context(),
		"UPDATE deployments SET status='cleanup_unconfirmed' WHERE id=?",
		execution.DeploymentID,
	); err != nil {
		t.Fatal(err)
	}

	// When
	resp, err := h.authedClient().PostForm(fmt.Sprintf(
		"%s/projects/%d/runbooks/executions/%d/retry",
		h.server.URL, project.ID, execution.ID),
		url.Values{"csrf_token": {h.csrfToken()}})
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// Then
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

package api_test

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/server"
	"github.com/robfig/cron/v3"
)

func TestRunbookDeleteAPI_RetainsDefinition(t *testing.T) {
	// Given: a saved runbook and an authenticated project writer.
	h := newAPIHarness(t, fakePodman(t))
	user := seedAPIUser(t, h.repo, "delete@example.test", "admin")
	_, token := seedAPIToken(t, h.repo, user.ID)
	project := seedProject(t, h.repo)
	book, version, err := h.repo.SaveRunbook(
		t.Context(),
		repository.RunbookSave{
			ProjectID: project.ID, Name: "obsolete", StepsJSON: "[]",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	router := server.NewRouter(h.repo, h.runner,
		cron.NewParser(cron.Minute|cron.Hour|cron.Dom|cron.Month|cron.Dow),
		handler.NewAuthHandler(h.repo))
	request := httptest.NewRequest(
		http.MethodDelete,
		fmt.Sprintf(
			"/api/v1/projects/%d/runbooks/%d",
			project.ID,
			book.ID,
		),
		nil,
	)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()

	// When: the writer deletes the runbook through the public API.
	router.ServeHTTP(response, request)

	// Then: deletion hides the runbook and preserves its immutable version.
	if response.Code != http.StatusNoContent || response.Body.Len() != 0 {
		t.Fatalf(
			"delete status=%d body=%s",
			response.Code,
			response.Body.String(),
		)
	}
	_, err = h.repo.Queries.GetRunbook(t.Context(), db.GetRunbookParams{
		ID: book.ID, ProjectID: project.ID,
	})
	if !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("live runbook lookup=%v", err)
	}
	if _, err := h.repo.Queries.GetRunbookVersion(t.Context(),
		db.GetRunbookVersionParams{ID: version.ID, RunbookID: book.ID}); err != nil {
		t.Fatalf("retained version: %v", err)
	}
}

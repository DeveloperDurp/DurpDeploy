package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/runner"
	"durpdeploy/views/pages"
)

type RunbookHandler struct {
	repo   *repository.Repository
	runner *runner.DeploymentRunner
}

func NewRunbookHandler(
	repo *repository.Repository,
	r *runner.DeploymentRunner,
) *RunbookHandler {
	return &RunbookHandler{repo: repo, runner: r}
}

func (h *RunbookHandler) List(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", http.StatusBadRequest)
		return
	}
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	books, err := h.repo.Queries.ListRunbooks(r.Context(), projectID)
	if err != nil {
		http.Error(w, "Cannot list runbooks", http.StatusInternalServerError)
		return
	}
	executions, err := h.repo.Queries.ListRunbookExecutions(
		r.Context(),
		projectID,
	)
	if err != nil {
		http.Error(w, "Cannot list executions", http.StatusInternalServerError)
		return
	}
	if err := pages.RunbooksPage(project, books, executions, r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(w, "Cannot render runbooks", http.StatusInternalServerError)
	}
}

func (h *RunbookHandler) Form(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", http.StatusBadRequest)
		return
	}
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	book := db.Runbook{}
	stepsJSON := `[{
		"name":"","script_body":"","interpreter":"bash",
		"timeout_seconds":0,"max_retries":0,
		"execution_target":"local","agent_selectors":[]
	}]`
	if idStr := chi.URLParam(r, "runbookId"); idStr != "" {
		id, err := strconv.ParseInt(idStr, 10, 64)
		if err != nil {
			http.Error(w, "Invalid runbook", http.StatusBadRequest)
			return
		}
		book, err = h.repo.Queries.GetRunbook(r.Context(), db.GetRunbookParams{
			ID: id, ProjectID: projectID,
		})
		if err != nil {
			http.NotFound(w, r)
			return
		}
		version, err := h.repo.Queries.GetLatestRunbookVersion(
			r.Context(),
			book.ID,
		)
		if err != nil {
			http.Error(w, "Cannot read version", http.StatusInternalServerError)
			return
		}
		release, err := h.repo.Queries.GetRelease(
			r.Context(),
			version.ReleaseID,
		)
		if err != nil {
			http.Error(w, "Cannot read steps", http.StatusInternalServerError)
			return
		}
		stepsJSON = release.StepsJson
	}
	if err := pages.RunbookFormPage(project, book, stepsJSON, "", r.URL.Path).
		Render(r.Context(), w); err != nil {
		http.Error(
			w,
			"Cannot render runbook form",
			http.StatusInternalServerError,
		)
	}
}

func (h *RunbookHandler) Save(w http.ResponseWriter, r *http.Request) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	stepsJSON, err := h.formSteps(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	id := int64(0)
	if idStr := chi.URLParam(r, "runbookId"); idStr != "" {
		id, err = strconv.ParseInt(idStr, 10, 64)
		if err != nil || id <= 0 {
			http.Error(w, "Invalid runbook", http.StatusBadRequest)
			return
		}
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if id == 0 && name == "" {
		http.Error(w, "Name is required", http.StatusUnprocessableEntity)
		return
	}
	book, _, err := h.repo.SaveRunbook(r.Context(), repository.RunbookSave{
		ProjectID: projectID, RunbookID: id, Name: name,
		Description: r.FormValue("description"), StepsJSON: stepsJSON,
	})
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(
			w,
			fmt.Sprintf("Cannot save runbook: %v", err),
			http.StatusUnprocessableEntity,
		)
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/runbooks/%d", projectID, book.ID),
		http.StatusSeeOther,
	)
}

func (h *RunbookHandler) environments(
	ctx context.Context,
	project db.Project,
) ([]db.Environment, error) {
	all, err := h.repo.Queries.ListEnvironments(ctx)
	if err != nil || !project.LifecycleID.Valid {
		return all, err
	}
	stages, err := h.repo.Queries.ListLifecycleStages(
		ctx,
		project.LifecycleID.Int64,
	)
	if err != nil {
		return nil, err
	}
	allowed := make(map[int64]bool, len(stages))
	for _, stage := range stages {
		allowed[stage.EnvironmentID] = true
	}
	visible := make([]db.Environment, 0, len(all))
	for _, environment := range all {
		if allowed[environment.ID] {
			visible = append(visible, environment)
		}
	}
	return visible, nil
}

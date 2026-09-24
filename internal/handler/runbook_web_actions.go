package handler

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/gate"
	"durpdeploy/internal/repository"
	"durpdeploy/views/pages"
)

func (h *RunbookHandler) book(
	w http.ResponseWriter,
	r *http.Request,
) (db.Project, db.Runbook, bool) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project", http.StatusBadRequest)
		return db.Project{}, db.Runbook{}, false
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "runbookId"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid runbook", http.StatusBadRequest)
		return db.Project{}, db.Runbook{}, false
	}
	project, err := h.repo.Queries.GetProject(r.Context(), projectID)
	if err != nil {
		http.NotFound(w, r)
		return db.Project{}, db.Runbook{}, false
	}
	book, err := h.repo.Queries.GetRunbook(r.Context(), db.GetRunbookParams{
		ID: id, ProjectID: projectID,
	})
	if err != nil {
		http.NotFound(w, r)
		return db.Project{}, db.Runbook{}, false
	}
	return project, book, true
}

func (h *RunbookHandler) Detail(w http.ResponseWriter, r *http.Request) {
	project, book, ok := h.book(w, r)
	if !ok {
		return
	}
	versions, err := h.repo.Queries.ListRunbookVersions(r.Context(), book.ID)
	if err != nil {
		http.Error(w, "Cannot list versions", http.StatusInternalServerError)
		return
	}
	schedules, err := h.repo.Queries.ListRunbookSchedules(r.Context(), book.ID)
	if err != nil {
		http.Error(w, "Cannot list schedules", http.StatusInternalServerError)
		return
	}
	environments, err := h.environments(r.Context(), project)
	if err != nil {
		http.Error(
			w,
			"Cannot list environments",
			http.StatusInternalServerError,
		)
		return
	}
	if err := pages.RunbookDetailPage(project, book, versions, schedules,
		environments, r.URL.Path).Render(r.Context(), w); err != nil {
		http.Error(w, "Cannot render runbook", http.StatusInternalServerError)
	}
}

func (h *RunbookHandler) parseExecutionEnvironment(
	w http.ResponseWriter, r *http.Request, project db.Project,
) (int64, bool) {
	id, err := strconv.ParseInt(r.FormValue("environment_id"), 10, 64)
	if err != nil || id <= 0 {
		http.Error(w, "Invalid environment", http.StatusUnprocessableEntity)
		return 0, false
	}
	environments, err := h.environments(r.Context(), project)
	if err != nil {
		http.Error(
			w,
			"Cannot read environments",
			http.StatusInternalServerError,
		)
		return 0, false
	}
	for _, environment := range environments {
		if environment.ID == id {
			return id, true
		}
	}
	http.Error(
		w,
		"Environment is not accessible",
		http.StatusUnprocessableEntity,
	)
	return 0, false
}

func (h *RunbookHandler) Execute(w http.ResponseWriter, r *http.Request) {
	project, book, ok := h.book(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	environmentID, ok := h.parseExecutionEnvironment(w, r, project)
	if !ok {
		return
	}
	versionID, err := strconv.ParseInt(r.FormValue("version_id"), 10, 64)
	if err != nil || versionID < 0 {
		http.Error(w, "Invalid version", http.StatusUnprocessableEntity)
		return
	}
	requiresApproval, err := gate.RequiresApproval(
		r.Context(),
		h.repo,
		project,
		environmentID,
	)
	if err != nil {
		http.Error(w, "Cannot check approval", http.StatusInternalServerError)
		return
	}
	status := "pending"
	if requiresApproval {
		status = "pending_approval"
	}
	user := auth.UserFromContext(r.Context())
	actor := sql.NullInt64{}
	if user != nil {
		actor = sql.NullInt64{Int64: user.ID, Valid: true}
	}
	execution, result, err := h.repo.CreateRunbookExecution(r.Context(),
		repository.RunbookExecutionRequest{
			ProjectID: project.ID, RunbookID: book.ID,
			VersionID: versionID, EnvironmentID: environmentID,
			ActorUserID: actor, Status: status,
		})
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		http.Error(w, "Cannot execute runbook", http.StatusInternalServerError)
		return
	}
	if status == "pending" {
		go h.runner.Run(context.Background(), result.Deployment.ID,
			result.Deployment.ReleaseID, result.Deployment.EnvironmentID)
	}
	http.Redirect(w, r, fmt.Sprintf("/projects/%d/runbooks/executions/%d",
		project.ID, execution.ID), http.StatusSeeOther)
}

func (h *RunbookHandler) Schedule(w http.ResponseWriter, r *http.Request) {
	project, book, ok := h.book(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	environmentID, ok := h.parseExecutionEnvironment(w, r, project)
	if !ok {
		return
	}
	versionID, err := strconv.ParseInt(r.FormValue("version_id"), 10, 64)
	if err != nil || versionID < 0 {
		http.Error(w, "Invalid version", http.StatusUnprocessableEntity)
		return
	}
	version := sql.NullInt64{}
	if versionID != 0 {
		if _, err := h.repo.Queries.GetRunbookVersion(
			r.Context(),
			db.GetRunbookVersionParams{
				ID:        versionID,
				RunbookID: book.ID,
			},
		); err != nil {
			http.NotFound(w, r)
			return
		}
		version = sql.NullInt64{Int64: versionID, Valid: true}
	}
	parser := cron.NewParser(
		cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
	)
	parsed, err := parser.Parse(r.FormValue("cron"))
	if err != nil {
		http.Error(w, "Invalid cron", http.StatusUnprocessableEntity)
		return
	}
	if _, err := h.repo.Queries.CreateRunbookSchedule(r.Context(),
		db.CreateRunbookScheduleParams{
			RunbookID: book.ID, VersionID: version,
			EnvironmentID: environmentID, Cron: r.FormValue("cron"),
			NextRunAt: parsed.Next(time.Now()).Unix(),
		}); err != nil {
		http.Error(w, "Cannot create schedule", http.StatusInternalServerError)
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/runbooks/%d", project.ID, book.ID),
		http.StatusSeeOther,
	)
}

func (h *RunbookHandler) DisableSchedule(
	w http.ResponseWriter,
	r *http.Request,
) {
	project, book, ok := h.book(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(chi.URLParam(r, "scheduleId"), 10, 64)
	if err != nil {
		http.Error(w, "Invalid schedule", http.StatusBadRequest)
		return
	}
	if _, err := h.repo.Queries.GetRunbookSchedule(r.Context(),
		db.GetRunbookScheduleParams{ID: id, RunbookID: book.ID}); err != nil {
		http.NotFound(w, r)
		return
	}
	if err := h.repo.Queries.DisableRunbookSchedule(
		r.Context(),
		id,
	); err != nil {
		http.Error(w, "Cannot disable schedule", http.StatusInternalServerError)
		return
	}
	http.Redirect(
		w,
		r,
		fmt.Sprintf("/projects/%d/runbooks/%d", project.ID, book.ID),
		http.StatusSeeOther,
	)
}

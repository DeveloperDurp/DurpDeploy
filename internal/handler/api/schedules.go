package api

import (
	"database/sql"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

var scheduleParser = cron.NewParser(
	cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow,
)

type ScheduleHandler struct {
	repo *repository.Repository
}

func NewScheduleHandler(repo *repository.Repository) *ScheduleHandler {
	return &ScheduleHandler{repo: repo}
}

func NewScheduledDeploymentHandler(
	repo *repository.Repository,
	_ cron.Parser,
) *ScheduleHandler {
	return NewScheduleHandler(repo)
}

type scheduledDeploymentRequest struct {
	ReleaseID     int64  `json:"release_id"`
	EnvironmentID int64  `json:"environment_id"`
	Cron          string `json:"cron"`
	CronExpr      string `json:"cron_expr"`
	Enabled       bool   `json:"enabled"`
	Active        bool   `json:"active"`
	Note          string `json:"note"`
}

func parseAndValidateCron(expr string) (cron.Schedule, error) {
	if strings.HasPrefix(expr, "TZ=") || strings.HasPrefix(expr, "CRON_TZ=") {
		return nil, sql.ErrNoRows // re-used as a sentinel
	}
	sched, err := scheduleParser.Parse(expr)
	if err != nil {
		return nil, err
	}
	if sched.Next(time.Now()).IsZero() {
		return nil, sql.ErrNoRows
	}
	return sched, nil
}

// swagger:route GET /projects/{id}/schedules schedules listSchedules
//
// List scheduled deployments for a project.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:ScheduledDeploymentListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *ScheduleHandler) ListSchedules(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	if _, err := h.repo.Queries.GetProject(r.Context(), projectID); err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Project not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	schedules, err := h.repo.Queries.ListScheduledDeploymentsByProjectPaginated(
		r.Context(),
		db.ListScheduledDeploymentsByProjectPaginatedParams{
			ProjectID: projectID,
			Limit:     limit,
			Offset:    offset,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountScheduledDeploymentsByProject(
		r.Context(),
		projectID,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]any, len(schedules))
	for i, s := range schedules {
		items[i] = s
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route POST /projects/{id}/schedules schedules createSchedule
//
// Create a scheduled deployment.
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  201: body:ScheduledDeployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *ScheduleHandler) CreateSchedule(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req scheduledDeploymentRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	cronExpr := req.CronExpr
	if cronExpr == "" {
		cronExpr = req.Cron
	}
	if req.ReleaseID == 0 || req.EnvironmentID == 0 || cronExpr == "" {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"release_id, environment_id, and cron are required",
		)
		return
	}

	release, err := h.repo.Queries.GetRelease(r.Context(), req.ReleaseID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Release not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if release.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Release not found")
		return
	}
	if _, err := h.repo.Queries.GetEnvironment(
		r.Context(),
		req.EnvironmentID,
	); err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Environment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	sched, err := parseAndValidateCron(cronExpr)
	if err != nil {
		RespondError(
			w,
			http.StatusBadRequest,
			"Invalid or unsatisfiable cron expression",
		)
		return
	}

	var enabled int64
	if req.Active || req.Enabled {
		enabled = 1
	}

	note := sql.NullString{}
	if req.Note != "" {
		note = sql.NullString{String: req.Note, Valid: true}
	}

	schedule, err := h.repo.Queries.CreateScheduledDeployment(
		r.Context(),
		db.CreateScheduledDeploymentParams{
			ProjectID:     projectID,
			ReleaseID:     req.ReleaseID,
			EnvironmentID: req.EnvironmentID,
			Cron:          cronExpr,
			NextRunAt:     sched.Next(time.Now()).Unix(),
			Enabled:       enabled,
			LastFiredAt:   sql.NullInt64{},
			Note:          note,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusCreated, schedule)
}

// swagger:route GET /projects/{id}/schedules/{schedId} schedules getSchedule
//
// Get a scheduled deployment.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:ScheduledDeployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *ScheduleHandler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	schedID, err := parseParamInt(r, "schedId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid schedule ID")
		return
	}

	schedule, err := h.repo.Queries.GetScheduledDeployment(r.Context(), schedID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Schedule not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// R2: refuse to serve a schedule that belongs to a different
	// project than the URL {id}. 404 (not 403) so we don't leak
	// existence.
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	if schedule.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Schedule not found")
		return
	}

	RespondJSON(w, http.StatusOK, schedule)
}

// swagger:route PUT /projects/{id}/schedules/{schedId} schedules updateSchedule
//
// Update a scheduled deployment.
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:ScheduledDeployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *ScheduleHandler) UpdateSchedule(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	schedID, err := parseParamInt(r, "schedId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid schedule ID")
		return
	}

	// R2: load the schedule first so we can verify it belongs to the
	// URL's project before mutating it. UpdateScheduledDeployment
	// returns the row only on success, so the existence check has to
	// happen up front.
	existing, err := h.repo.Queries.GetScheduledDeployment(r.Context(), schedID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Schedule not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if existing.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Schedule not found")
		return
	}

	var req scheduledDeploymentRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	cronExpr := req.CronExpr
	if cronExpr == "" {
		cronExpr = req.Cron
	}
	if req.ReleaseID == 0 || req.EnvironmentID == 0 || cronExpr == "" {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"release_id, environment_id, and cron are required",
		)
		return
	}
	release, err := h.repo.Queries.GetRelease(r.Context(), req.ReleaseID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Release not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if release.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Release not found")
		return
	}
	sched, err := parseAndValidateCron(cronExpr)
	if err != nil {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"Invalid or unsatisfiable cron expression",
		)
		return
	}

	var enabled int64
	if req.Active || req.Enabled {
		enabled = 1
	}

	note := sql.NullString{}
	if req.Note != "" {
		note = sql.NullString{String: req.Note, Valid: true}
	}

	schedule, err := h.repo.Queries.UpdateScheduledDeployment(
		r.Context(),
		db.UpdateScheduledDeploymentParams{
			ID:            schedID,
			ProjectID:     projectID,
			ReleaseID:     req.ReleaseID,
			EnvironmentID: req.EnvironmentID,
			Cron:          cronExpr,
			NextRunAt:     sched.Next(time.Now()).Unix(),
			Enabled:       enabled,
			LastFiredAt:   sql.NullInt64{},
			Note:          note,
		},
	)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Schedule not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, schedule)
}

// swagger:route DELETE /projects/{id}/schedules/{schedId} schedules deleteSchedule
//
// Delete a scheduled deployment.
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  204: body:EmptyResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *ScheduleHandler) DeleteSchedule(
	w http.ResponseWriter,
	r *http.Request,
) {
	schedID, err := parseParamInt(r, "schedId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid schedule ID")
		return
	}

	// R2: refuse to delete a schedule that belongs to a different
	// project than the URL {id}.
	existing, err := h.repo.Queries.GetScheduledDeployment(r.Context(), schedID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Schedule not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	if existing.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Schedule not found")
		return
	}

	if err := h.repo.Queries.DeleteScheduledDeployment(
		r.Context(),
		schedID,
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// swagger:route POST /projects/{id}/schedules/{schedId}/toggle schedules toggleSchedule
//
// Toggle active state of a scheduled deployment.
//
//	Produces:
//	- application/json
//
//	Schemes: http, https
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:ScheduledDeployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *ScheduleHandler) ToggleSchedule(
	w http.ResponseWriter,
	r *http.Request,
) {
	schedID, err := parseParamInt(r, "schedId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid schedule ID")
		return
	}

	// R2: refuse to toggle a schedule that belongs to a different
	// project than the URL {id}. ToggleScheduledDeploymentEnabled
	// returns the updated row only on success, so the existence
	// check has to happen up front.
	existing, err := h.repo.Queries.GetScheduledDeployment(r.Context(), schedID)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Schedule not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	if existing.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Schedule not found")
		return
	}

	updated, err := h.repo.Queries.ToggleScheduledDeploymentEnabled(
		r.Context(),
		schedID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Schedule not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, updated)
}

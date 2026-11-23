package api

import (
	"cmp"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

const invalidScheduleIDMessage = "Invalid schedule ID"
const scheduleNotFoundMessage = "Schedule not found"

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

// swagger:route GET /projects/{id}/schedules schedules listSchedules
//
// List scheduled deployments for a project.
//
//	Produces:
//	- application/json
//
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
	projectID, limit, offset, ok := projectPageParams(w, r, h.repo)
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
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
//	  201: body:ScheduledDeployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
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

	req, cronExpr, ok := h.readScheduleRequest(w, r, projectID)
	if !ok {
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
	sched, err := handler.ParseAndValidateCron(cronExpr)
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

	schedule, err := h.repo.CreateScheduledDeployment(
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
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, releaseNotFoundMessage)
			return
		}
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
//
//	Security:
//	  bearer:
//
//	Responses:
//
// 413: body:RequestEntityTooLargeError
//
//	200: body:ScheduledDeployment
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	404: body:NotFoundError
//	500: body:ServerError
func (h *ScheduleHandler) GetSchedule(w http.ResponseWriter, r *http.Request) {
	existing, ok := h.loadProjectSchedule(w, r)
	if !ok {
		return
	}

	RespondJSON(w, http.StatusOK, existing)
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
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
//	  200: body:ScheduledDeployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
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
	existing, ok := h.loadSchedule(w, r)
	if !ok {
		return
	}
	if existing.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, scheduleNotFoundMessage)
		return
	}

	req, cronExpr, ok := h.readScheduleRequest(w, r, projectID)
	if !ok {
		return
	}
	sched, err := handler.ParseAndValidateCron(cronExpr)
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

	schedule, err := h.repo.UpdateScheduledDeployment(
		r.Context(),
		db.UpdateScheduledDeploymentParams{
			ID:            existing.ID,
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
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(
				w,
				http.StatusNotFound,
				"Release or schedule not found",
			)
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
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
//	  204: body:EmptyResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *ScheduleHandler) DeleteSchedule(
	w http.ResponseWriter,
	r *http.Request,
) {
	existing, ok := h.loadProjectSchedule(w, r)
	if !ok {
		return
	}

	if err := h.repo.Queries.DeleteScheduledDeployment(
		r.Context(),
		existing.ID,
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
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
//	  200: body:ScheduledDeployment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *ScheduleHandler) ToggleSchedule(
	w http.ResponseWriter,
	r *http.Request,
) {
	existing, ok := h.loadProjectSchedule(w, r)
	if !ok {
		return
	}
	if existing.Enabled == 0 &&
		!h.validateScheduleRelease(w, r, existing.ReleaseID) {
		return
	}

	updated, err := h.repo.Queries.ToggleScheduledDeploymentEnabled(
		r.Context(),
		existing.ID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, scheduleNotFoundMessage)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, updated)
}

func (h *ScheduleHandler) readScheduleRequest(
	w http.ResponseWriter,
	r *http.Request,
	projectID int64,
) (scheduledDeploymentRequest, string, bool) {
	var req scheduledDeploymentRequest
	if !readJSONBool(w, r, &req) {
		return req, "", false
	}
	cronExpr := cmp.Or(req.CronExpr, req.Cron)
	if req.ReleaseID == 0 || req.EnvironmentID == 0 || cronExpr == "" {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"release_id, environment_id, and cron are required",
		)
		return req, "", false
	}

	release, err := h.repo.Queries.GetDeploymentRelease(
		r.Context(),
		req.ReleaseID,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, releaseNotFoundMessage)
			return req, "", false
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return req, "", false
	}
	if release.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, releaseNotFoundMessage)
		return req, "", false
	}
	if !h.validateScheduleRelease(w, r, release.ID) {
		return req, "", false
	}
	return req, cronExpr, true
}

func (h *ScheduleHandler) validateScheduleRelease(
	w http.ResponseWriter,
	r *http.Request,
	releaseID int64,
) bool {
	if err := h.repo.ValidateExecutableRelease(
		r.Context(),
		releaseID,
	); err != nil {
		if writeArtifactError(w, err) {
			return false
		}
		if errors.Is(err, repository.ErrLegacyServerStep) {
			RespondError(
				w,
				http.StatusConflict,
				repository.ErrLegacyServerStep.Error(),
			)
		} else {
			RespondError(w, http.StatusInternalServerError, err.Error())
		}
		return false
	}
	return true
}

func (h *ScheduleHandler) loadSchedule(
	w http.ResponseWriter,
	r *http.Request,
) (db.ScheduledDeployment, bool) {
	id, err := parseParamInt(r, "schedId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, invalidScheduleIDMessage)
		return db.ScheduledDeployment{}, false
	}

	schedule, err := h.repo.Queries.GetScheduledDeployment(r.Context(), id)
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, scheduleNotFoundMessage)
			return db.ScheduledDeployment{}, false
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return db.ScheduledDeployment{}, false
	}
	return schedule, true
}

func (h *ScheduleHandler) loadProjectSchedule(
	w http.ResponseWriter,
	r *http.Request,
) (db.ScheduledDeployment, bool) {
	schedule, ok := h.loadSchedule(w, r)
	if !ok {
		return db.ScheduledDeployment{}, false
	}
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return db.ScheduledDeployment{}, false
	}
	// Mask a schedule in another project as missing.
	if schedule.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, scheduleNotFoundMessage)
		return db.ScheduledDeployment{}, false
	}
	return schedule, true
}

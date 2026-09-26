package api

import (
	"database/sql"
	"errors"
	"net/http"
	"time"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
)

type runbookScheduleRequest struct {
	EnvironmentID int64  `json:"environment_id"`
	VersionID     int64  `json:"version_id"`
	Cron          string `json:"cron"`
}

// swagger:route GET /projects/{id}/runbooks/{runbookId}/schedules runbooks listRunbookSchedules
// List runbook schedules.
//
// Responses:
// 200: body:RunbookScheduleListResponse
func (h *RunbookHandler) ListSchedules(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "runbookId")
	if !ok {
		return
	}
	if _, err := h.repo.Queries.GetRunbook(r.Context(), db.GetRunbookParams{
		ID: id, ProjectID: projectID,
	}); err != nil {
		RespondError(w, http.StatusNotFound, "Runbook not found")
		return
	}
	items, err := h.repo.Queries.ListRunbookSchedules(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot list schedules")
		return
	}
	RespondJSON(w, http.StatusOK, items)
}

// swagger:route POST /projects/{id}/runbooks/{runbookId}/schedules runbooks createRunbookSchedule
// Schedule a pinned version or the latest saved version.
//
// Consumes:
// - application/json
//
// Responses:
// 201: body:RunbookSchedule
// 422: body:ValidationError
func (h *RunbookHandler) CreateSchedule(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "runbookId")
	if !ok {
		return
	}
	var req runbookScheduleRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	if req.EnvironmentID <= 0 || req.VersionID < 0 {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"Invalid environment or version",
		)
		return
	}
	if _, err := h.repo.Queries.GetRunbook(r.Context(), db.GetRunbookParams{
		ID: id, ProjectID: projectID,
	}); err != nil {
		RespondError(w, http.StatusNotFound, "Runbook not found")
		return
	}
	if _, ok := h.projectEnvironment(w, r, projectID, req.EnvironmentID); !ok {
		return
	}
	version := sql.NullInt64{}
	if req.VersionID != 0 {
		if _, err := h.repo.Queries.GetRunbookVersion(
			r.Context(),
			db.GetRunbookVersionParams{
				ID:        req.VersionID,
				RunbookID: id,
			},
		); err != nil {
			RespondError(w, http.StatusNotFound, "Version not found")
			return
		}
		version = sql.NullInt64{Int64: req.VersionID, Valid: true}
	}
	parsed, err := handler.ParseAndValidateCron(req.Cron)
	if err != nil {
		RespondError(
			w,
			http.StatusUnprocessableEntity,
			"Invalid cron expression",
		)
		return
	}
	schedule, err := h.repo.Queries.CreateRunbookSchedule(r.Context(),
		db.CreateRunbookScheduleParams{
			RunbookID: id, VersionID: version,
			EnvironmentID: req.EnvironmentID,
			Cron:          req.Cron, NextRunAt: parsed.Next(time.Now()).Unix(),
		})
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Cannot create schedule",
		)
		return
	}
	RespondJSON(w, http.StatusCreated, schedule)
}

// swagger:route POST /projects/{id}/runbooks/{runbookId}/schedules/{scheduleId}/disable runbooks disableRunbookSchedule
// Disable a runbook schedule.
//
// Responses:
// 200: body:RunbookScheduleStateResponse
func (h *RunbookHandler) DisableSchedule(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "runbookId")
	if !ok {
		return
	}
	scheduleID, ok := parseIDParam(w, r, "scheduleId")
	if !ok {
		return
	}
	if _, err := h.repo.Queries.GetRunbook(r.Context(), db.GetRunbookParams{
		ID: id, ProjectID: projectID,
	}); err != nil {
		RespondError(w, http.StatusNotFound, "Runbook not found")
		return
	}
	if _, err := h.repo.Queries.GetRunbookSchedule(
		r.Context(),
		db.GetRunbookScheduleParams{
			ID:        scheduleID,
			RunbookID: id,
		},
	); errors.Is(err, sql.ErrNoRows) {
		RespondError(w, http.StatusNotFound, "Schedule not found")
		return
	} else if err != nil {
		RespondError(w, http.StatusInternalServerError, "Cannot read schedule")
		return
	}
	if err := h.repo.Queries.DisableRunbookSchedule(
		r.Context(),
		scheduleID,
	); err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Cannot disable schedule",
		)
		return
	}
	RespondJSON(w, http.StatusOK, map[string]bool{"enabled": false})
}

package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/robfig/cron/v3"

	"durpdeploy/internal/db"
	"durpdeploy/internal/dispatch"
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
	TargetMode    string `json:"target_mode"`
	AgentLabelID  int64  `json:"agent_label_id"`
	AgentStrategy string `json:"agent_strategy"`
}

type scheduledDeploymentResponse struct {
	db.ScheduledDeployment
	TargetMode    string `json:"target_mode"`
	AgentLabelID  *int64 `json:"agent_label_id"`
	AgentStrategy string `json:"agent_strategy,omitempty"`
}

func (h *ScheduleHandler) response(
	r *http.Request,
	schedule db.ScheduledDeployment,
) (scheduledDeploymentResponse, error) {
	response := scheduledDeploymentResponse{
		ScheduledDeployment: schedule, TargetMode: "inherit",
	}
	policy, err := h.repo.Queries.GetScheduledDeploymentRoutingPolicy(
		r.Context(), schedule.ID,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return response, nil
	}
	if err != nil {
		return scheduledDeploymentResponse{}, err
	}
	response.TargetMode = policy.TargetMode
	if policy.AgentLabelID.Valid {
		response.AgentLabelID = &policy.AgentLabelID.Int64
	}
	response.AgentStrategy = policy.AgentStrategy.String
	return response, nil
}

func (request scheduledDeploymentRequest) routingInput() (dispatch.Input, error) {
	mode := request.TargetMode
	if mode == "" {
		mode = "inherit"
	}
	input := dispatch.Input{
		Source: dispatch.SourceSchedule, Mode: mode,
		LabelID: request.AgentLabelID, Strategy: request.AgentStrategy,
	}
	if _, err := dispatch.ParseInput(input); err != nil {
		return dispatch.Input{}, err
	}
	return input, nil
}

func apiSchedulePolicyParams(
	scheduleID int64,
	input dispatch.Input,
) db.CreateScheduledDeploymentRoutingPolicyParams {
	params := db.CreateScheduledDeploymentRoutingPolicyParams{
		ScheduledDeploymentID: scheduleID, TargetMode: input.Mode,
	}
	if input.Mode == "label" {
		params.AgentLabelID = sql.NullInt64{Int64: input.LabelID, Valid: true}
		params.AgentStrategy = sql.NullString{String: input.Strategy, Valid: true}
	}
	return params
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
		response, err := h.response(r, s)
		if err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
		items[i] = response
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
	routingInput, err := req.routingInput()
	if err != nil {
		RespondError(w, http.StatusUnprocessableEntity, "Invalid execution target")
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

	var schedule db.ScheduledDeployment
	err = h.repo.WithRoutingTx(r.Context(), func(q *db.Queries) error {
		var err error
		schedule, err = q.CreateScheduledDeployment(
			r.Context(), db.CreateScheduledDeploymentParams{
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
			return err
		}
		_, err = q.CreateScheduledDeploymentRoutingPolicy(
			r.Context(), apiSchedulePolicyParams(schedule.ID, routingInput),
		)
		return err
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response, err := h.response(r, schedule)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusCreated, response)
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

	response, err := h.response(r, schedule)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, response)
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
	routingInput, err := req.routingInput()
	if err != nil {
		RespondError(w, http.StatusUnprocessableEntity, "Invalid execution target")
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

	var schedule db.ScheduledDeployment
	err = h.repo.WithRoutingTx(r.Context(), func(q *db.Queries) error {
		var err error
		schedule, err = q.UpdateScheduledDeployment(
			r.Context(), db.UpdateScheduledDeploymentParams{
				ID:            schedID,
				ProjectID:     projectID,
				ReleaseID:     req.ReleaseID,
				EnvironmentID: req.EnvironmentID,
				Cron:          cronExpr,
				NextRunAt:     sched.Next(time.Now()).Unix(),
				Enabled:       enabled,
				LastFiredAt:   existing.LastFiredAt,
				Note:          note,
			},
		)
		if err != nil {
			return err
		}
		params := apiSchedulePolicyParams(schedID, routingInput)
		_, err = q.UpdateScheduledDeploymentRoutingPolicy(
			r.Context(), db.UpdateScheduledDeploymentRoutingPolicyParams{
				TargetMode: params.TargetMode, AgentLabelID: params.AgentLabelID,
				AgentStrategy:         params.AgentStrategy,
				ScheduledDeploymentID: schedID,
			},
		)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = q.CreateScheduledDeploymentRoutingPolicy(r.Context(), params)
		}
		return err
	})
	if err != nil {
		if err == sql.ErrNoRows {
			RespondError(w, http.StatusNotFound, "Schedule not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	response, err := h.response(r, schedule)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, response)
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

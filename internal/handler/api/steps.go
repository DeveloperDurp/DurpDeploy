package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type StepHandler struct {
	repo *repository.Repository
}

func NewStepHandler(repo *repository.Repository) *StepHandler {
	return &StepHandler{repo: repo}
}

type stepRequest struct {
	Name           string `json:"name"`
	ScriptBody     string `json:"script_body"`
	SortOrder      int64  `json:"sort_order"`
	TimeoutSeconds int64  `json:"timeout_seconds"`
	MaxRetries     int64  `json:"max_retries"`
}

type reorderStepsRequest struct {
	StepIDs []int64 `json:"step_ids"`
}

// swagger:route GET /projects/{id}/steps steps listSteps
//
// List steps for a project.
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
//	  200: body:StepListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *StepHandler) ListSteps(w http.ResponseWriter, r *http.Request) {
	projectID, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	steps, err := h.repo.Queries.ListStepsByProjectPaginated(
		r.Context(),
		db.ListStepsByProjectPaginatedParams{
			ProjectID: projectID,
			Limit:     limit,
			Offset:    offset,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountStepsByProject(r.Context(), projectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]any, len(steps))
	for i, s := range steps {
		items[i] = s
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route POST /projects/{id}/steps steps createStep
//
// Create a project step.
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
//	  201: body:Step
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *StepHandler) CreateStep(w http.ResponseWriter, r *http.Request) {
	projectID, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req stepRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}
	if req.TimeoutSeconds < 0 {
		RespondError(
			w,
			http.StatusBadRequest,
			"Timeout must be a non-negative integer",
		)
		return
	}
	if req.MaxRetries < 0 {
		RespondError(
			w,
			http.StatusBadRequest,
			"Max retries must be a non-negative integer",
		)
		return
	}

	sortOrder := req.SortOrder
	if sortOrder <= 0 {
		steps, _ := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
		for _, s := range steps {
			if s.SortOrder >= sortOrder {
				sortOrder = s.SortOrder + 1
			}
		}
	}

	step, err := h.repo.Queries.CreateStep(r.Context(), db.CreateStepParams{
		ProjectID:      projectID,
		Name:           name,
		ScriptBody:     req.ScriptBody,
		SortOrder:      sortOrder,
		TimeoutSeconds: req.TimeoutSeconds,
		MaxRetries:     req.MaxRetries,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusCreated, step)
}

// swagger:route GET /projects/{id}/steps/{stepId} steps getStep
//
// Get a project step.
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
//	  200: body:Step
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *StepHandler) GetStep(w http.ResponseWriter, r *http.Request) {
	stepID, err := parseParamInt(r, "stepId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid step ID")
		return
	}

	step, err := h.repo.Queries.GetStep(r.Context(), stepID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Step not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// R2: refuse to serve a step that belongs to a different project
	// than the URL {id}. 404 (not 403) so we don't leak existence.
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	if step.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Step not found")
		return
	}

	RespondJSON(w, http.StatusOK, step)
}

// swagger:route PUT /projects/{id}/steps/{stepId} steps updateStep
//
// Update a project step.
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
//	  200: body:Step
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *StepHandler) UpdateStep(w http.ResponseWriter, r *http.Request) {
	stepID, err := parseParamInt(r, "stepId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid step ID")
		return
	}

	// R2: load the step first so we can verify it belongs to the URL's
	// project before mutating it. UpdateStep returns the row only on
	// success, so the existence check has to happen up front.
	existing, err := h.repo.Queries.GetStep(r.Context(), stepID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Step not found")
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
		RespondError(w, http.StatusNotFound, "Step not found")
		return
	}

	var req stepRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}
	if req.TimeoutSeconds < 0 {
		RespondError(
			w,
			http.StatusBadRequest,
			"Timeout must be a non-negative integer",
		)
		return
	}
	if req.MaxRetries < 0 {
		RespondError(
			w,
			http.StatusBadRequest,
			"Max retries must be a non-negative integer",
		)
		return
	}

	step, err := h.repo.Queries.UpdateStep(r.Context(), db.UpdateStepParams{
		ID:             stepID,
		Name:           name,
		ScriptBody:     req.ScriptBody,
		SortOrder:      req.SortOrder,
		TimeoutSeconds: req.TimeoutSeconds,
		MaxRetries:     req.MaxRetries,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Step not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, step)
}

// swagger:route DELETE /projects/{id}/steps/{stepId} steps deleteStep
//
// Delete a project step.
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
func (h *StepHandler) DeleteStep(w http.ResponseWriter, r *http.Request) {
	stepID, err := parseParamInt(r, "stepId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid step ID")
		return
	}

	// R2: refuse to delete a step that belongs to a different project
	// than the URL {id}. 404 (not 403) so we don't leak existence.
	existing, err := h.repo.Queries.GetStep(r.Context(), stepID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Step not found")
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
		RespondError(w, http.StatusNotFound, "Step not found")
		return
	}

	if err := h.repo.Queries.DeleteStep(r.Context(), stepID); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// swagger:route PATCH /projects/{id}/steps/reorder steps reorderSteps
//
// Reorder project steps.
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
//	  200: body:StepListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *StepHandler) ReorderSteps(w http.ResponseWriter, r *http.Request) {
	projectID, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req reorderStepsRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	steps, err := h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	existing := make(map[int64]db.Step, len(steps))
	for _, s := range steps {
		existing[s.ID] = s
	}
	if len(req.StepIDs) != len(existing) {
		RespondError(
			w,
			http.StatusBadRequest,
			"step_ids must include every step exactly once",
		)
		return
	}
	seen := make(map[int64]bool, len(req.StepIDs))
	for _, sid := range req.StepIDs {
		if _, ok := existing[sid]; !ok {
			RespondError(
				w,
				http.StatusBadRequest,
				"step_ids contains an unknown step",
			)
			return
		}
		if seen[sid] {
			RespondError(
				w,
				http.StatusBadRequest,
				"step_ids contains duplicates",
			)
			return
		}
		seen[sid] = true
	}

	tx, err := h.repo.DB.BeginTx(r.Context(), nil)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer tx.Rollback()
	qtx := h.repo.Queries.WithTx(tx)

	for i, sid := range req.StepIDs {
		s := existing[sid]
		if _, err := qtx.UpdateStep(r.Context(), db.UpdateStepParams{
			ID:             s.ID,
			Name:           s.Name,
			ScriptBody:     s.ScriptBody,
			SortOrder:      int64(i + 1),
			TimeoutSeconds: s.TimeoutSeconds,
			MaxRetries:     s.MaxRetries,
		}); err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	steps, err = h.repo.Queries.ListStepsByProject(r.Context(), projectID)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, steps)
}

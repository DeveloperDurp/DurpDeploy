package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
	"durpdeploy/internal/stepdefinition"
)

type StepHandler struct {
	repo *repository.Repository
}

func NewStepHandler(repo *repository.Repository) *StepHandler {
	return &StepHandler{repo: repo}
}

type stepRequest = stepdefinition.Definition

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

	responses, err := newStepResponses(r.Context(), h.repo, steps)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := make([]any, len(responses))
	for index, response := range responses {
		items[index] = response
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
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
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
	if !readJSONBool(w, r, &req) {
		return
	}

	prepared, ok := prepareStepFields(w, r, h.repo, req)
	if !ok {
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

	prepared.SortOrder = sortOrder
	step, err := h.repo.CreateStepWithPlacement(
		r.Context(),
		prepared.CreateStepParams(
			projectID,
			marshalVariableNames(prepared.VariableNames),
		),
		prepared.ExecutionTarget,
		prepared.AgentSelectors,
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response, err := newStepResponse(r.Context(), h.repo, step)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusCreated, response)
}

// swagger:route GET /projects/{id}/steps/{stepId} steps getStep
//
// Get a project step.
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
//	200: body:Step
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	404: body:NotFoundError
//	500: body:ServerError
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

	response, err := newStepResponse(r.Context(), h.repo, step)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, response)
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
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
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
	if !readJSONBool(w, r, &req) {
		return
	}

	prepared, ok := prepareStepFields(w, r, h.repo, req)
	if !ok {
		return
	}

	step, err := h.repo.UpdateStepWithPlacement(
		r.Context(),
		prepared.UpdateStepParams(
			stepID,
			marshalVariableNames(prepared.VariableNames),
		),
		prepared.ExecutionTarget,
		prepared.AgentSelectors,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Step not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	response, err := newStepResponse(r.Context(), h.repo, step)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, response)
}

// swagger:route DELETE /projects/{id}/steps/{stepId} steps deleteStep
//
// Delete a project step.
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
//
//	Security:
//	  bearer:
//
//	Responses:
//	  413: body:RequestEntityTooLargeError
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
	if !readJSONBool(w, r, &req) {
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
	defer func() {
		repository.LogRollbackError(tx.Rollback())
	}()
	qtx := h.repo.Queries.WithTx(tx)

	for i, sid := range req.StepIDs {
		s := existing[sid]
		if _, err := qtx.UpdateStep(r.Context(), db.UpdateStepParams{
			ID:                   s.ID,
			Name:                 s.Name,
			ScriptBody:           s.ScriptBody,
			SortOrder:            int64(i + 1),
			TimeoutSeconds:       s.TimeoutSeconds,
			MaxRetries:           s.MaxRetries,
			Interpreter:          s.Interpreter,
			ContainerImage:       s.ContainerImage,
			AgentExecutionMode:   s.AgentExecutionMode,
			NetworkMode:          s.NetworkMode,
			ApprovalArtifactPath: s.ApprovalArtifactPath,
			ApprovalReviewPath:   s.ApprovalReviewPath,
			ApprovalReviewFormat: s.ApprovalReviewFormat,
			VariableNames:        s.VariableNames,
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
	responses, err := newStepResponses(r.Context(), h.repo, steps)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, responses)
}

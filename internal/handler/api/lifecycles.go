package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type LifecycleHandler struct {
	repo *repository.Repository
}

func NewLifecycleHandler(repo *repository.Repository) *LifecycleHandler {
	return &LifecycleHandler{repo: repo}
}

type lifecycleRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type lifecycleStageRequest struct {
	EnvironmentID    *int64 `json:"environment_id"`
	SortOrder        *int64 `json:"sort_order"`
	RequiresApproval *bool  `json:"requires_approval"`
}

type reorderStagesRequest struct {
	StageIDs []int64 `json:"stage_ids"`
}

type lifecycleResponse struct {
	Lifecycle lifecycleData       `json:"lifecycle"`
	Stages    []db.LifecycleStage `json:"stages"`
}

type lifecycleData struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	CreatedAt   int64  `json:"created_at"`
}

func toLifecycleData(lc db.Lifecycle) lifecycleData {
	return lifecycleData{
		ID:          lc.ID,
		Name:        lc.Name,
		Description: lc.Description.String,
		CreatedAt:   lc.CreatedAt,
	}
}

// swagger:route GET /lifecycles lifecycles listLifecycles
//
// List all lifecycles.
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
//	  200: body:LifecycleListResponse
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *LifecycleHandler) ListLifecycles(
	w http.ResponseWriter,
	r *http.Request,
) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	lifecycles, err := h.repo.Queries.ListLifecyclesPaginated(
		r.Context(),
		db.ListLifecyclesPaginatedParams{
			Limit:  limit,
			Offset: offset,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountLifecycles(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]any, len(lifecycles))
	for i, lc := range lifecycles {
		items[i] = lc
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route POST /lifecycles lifecycles createLifecycle
//
// Create a new lifecycle.
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
//	  201: body:Lifecycle
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *LifecycleHandler) CreateLifecycle(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req lifecycleRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	lc, err := h.repo.Queries.CreateLifecycle(
		r.Context(),
		db.CreateLifecycleParams{
			Name: name,
			Description: sql.NullString{
				String: req.Description,
				Valid:  req.Description != "",
			},
		},
	)
	if err != nil {
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"A lifecycle with this name already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusCreated, toLifecycleData(lc))
}

// swagger:route GET /lifecycles/{id} lifecycles getLifecycle
//
// Get a lifecycle with its stages.
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
//	  200: body:LifecycleResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *LifecycleHandler) GetLifecycle(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid lifecycle ID")
		return
	}

	lc, err := h.repo.Queries.GetLifecycle(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Lifecycle not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stages, err := h.repo.Queries.ListLifecycleStages(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(
		w,
		http.StatusOK,
		lifecycleResponse{Lifecycle: toLifecycleData(lc), Stages: stages},
	)
}

// swagger:route POST /lifecycles/{id}/save lifecycles saveLifecycle
//
// Save a lifecycle name and description.
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
//	  200: body:LifecycleResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *LifecycleHandler) SaveLifecycle(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid lifecycle ID")
		return
	}

	var req lifecycleRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	lc, err := h.repo.Queries.UpdateLifecycle(
		r.Context(),
		db.UpdateLifecycleParams{
			ID:   id,
			Name: name,
			Description: sql.NullString{
				String: req.Description,
				Valid:  req.Description != "",
			},
		},
	)
	if err != nil {
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"A lifecycle with this name already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stages, err := h.repo.Queries.ListLifecycleStages(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(
		w,
		http.StatusOK,
		lifecycleResponse{Lifecycle: toLifecycleData(lc), Stages: stages},
	)
}

// swagger:route POST /lifecycles/{id}/stages lifecycles addLifecycleStage
//
// Add a stage to a lifecycle.
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
//	  201: body:LifecycleStage
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *LifecycleHandler) AddStage(w http.ResponseWriter, r *http.Request) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid lifecycle ID")
		return
	}

	var req lifecycleStageRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.EnvironmentID == nil || *req.EnvironmentID <= 0 {
		RespondError(w, http.StatusBadRequest, "environment_id is required")
		return
	}

	stages, err := h.repo.Queries.ListLifecycleStages(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, s := range stages {
		if s.EnvironmentID == *req.EnvironmentID {
			RespondError(
				w,
				http.StatusConflict,
				"Environment already in this lifecycle",
			)
			return
		}
	}

	sortOrder := int64(0)
	if req.SortOrder != nil && *req.SortOrder > 0 {
		sortOrder = *req.SortOrder
	}
	if sortOrder <= 0 {
		sortOrder = nextLifecycleSortOrder(stages)
	}

	requiresApproval := int64(0)
	if req.RequiresApproval != nil && *req.RequiresApproval {
		requiresApproval = 1
	}

	stage, err := h.repo.Queries.CreateLifecycleStage(
		r.Context(),
		db.CreateLifecycleStageParams{
			LifecycleID:      id,
			EnvironmentID:    *req.EnvironmentID,
			SortOrder:        sortOrder,
			RequiresApproval: requiresApproval,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusCreated, stage)
}

// swagger:route POST /lifecycles/{id}/stages/reorder lifecycles reorderLifecycleStages
//
// Reorder lifecycle stages.
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
//	  200: body:LifecycleStageListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *LifecycleHandler) ReorderStages(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid lifecycle ID")
		return
	}

	var req reorderStagesRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	stages, err := h.repo.Queries.ListLifecycleStages(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	existing := make(map[int64]db.LifecycleStage, len(stages))
	for _, s := range stages {
		existing[s.ID] = s
	}
	if len(req.StageIDs) != len(existing) {
		RespondError(
			w,
			http.StatusBadRequest,
			"stage_ids must include every stage exactly once",
		)
		return
	}
	seen := make(map[int64]bool, len(req.StageIDs))
	for _, sid := range req.StageIDs {
		if _, ok := existing[sid]; !ok {
			RespondError(
				w,
				http.StatusBadRequest,
				"stage_ids contains an unknown stage",
			)
			return
		}
		if seen[sid] {
			RespondError(
				w,
				http.StatusBadRequest,
				"stage_ids contains duplicates",
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

	for i, sid := range req.StageIDs {
		s := existing[sid]
		if _, err := qtx.UpdateLifecycleStage(
			r.Context(),
			db.UpdateLifecycleStageParams{
				ID:               s.ID,
				EnvironmentID:    s.EnvironmentID,
				SortOrder:        -int64(i + 1),
				RequiresApproval: s.RequiresApproval,
			},
		); err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	for i, sid := range req.StageIDs {
		s := existing[sid]
		if _, err := qtx.UpdateLifecycleStage(
			r.Context(),
			db.UpdateLifecycleStageParams{
				ID:               s.ID,
				EnvironmentID:    s.EnvironmentID,
				SortOrder:        int64(i + 1),
				RequiresApproval: s.RequiresApproval,
			},
		); err != nil {
			RespondError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if err := tx.Commit(); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	stages, err = h.repo.Queries.ListLifecycleStages(r.Context(), id)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	RespondJSON(w, http.StatusOK, stages)
}

// swagger:route PATCH /lifecycles/{id}/stages/{stageId} lifecycles updateLifecycleStage
//
// Update a lifecycle stage.
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
//	  200: body:LifecycleStage
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *LifecycleHandler) UpdateStage(w http.ResponseWriter, r *http.Request) {
	stageID, err := parseParamInt(r, "stageId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid stage ID")
		return
	}

	var req lifecycleStageRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	stage, err := h.repo.Queries.GetLifecycleStage(r.Context(), stageID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Stage not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	environmentID := stage.EnvironmentID
	if req.EnvironmentID != nil && *req.EnvironmentID > 0 {
		environmentID = *req.EnvironmentID
	}
	sortOrder := stage.SortOrder
	if req.SortOrder != nil && *req.SortOrder > 0 {
		sortOrder = *req.SortOrder
	}
	requiresApproval := stage.RequiresApproval
	if req.RequiresApproval != nil {
		requiresApproval = 0
		if *req.RequiresApproval {
			requiresApproval = 1
		}
	}

	updated, err := h.repo.Queries.UpdateLifecycleStage(
		r.Context(),
		db.UpdateLifecycleStageParams{
			ID:               stageID,
			EnvironmentID:    environmentID,
			SortOrder:        sortOrder,
			RequiresApproval: requiresApproval,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, updated)
}

// swagger:route POST /lifecycles/{id}/stages/{stageId}/delete lifecycles deleteLifecycleStage
//
// Delete a lifecycle stage.
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
func (h *LifecycleHandler) DeleteStage(w http.ResponseWriter, r *http.Request) {
	stageID, err := parseParamInt(r, "stageId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid stage ID")
		return
	}

	if err := h.repo.Queries.DeleteLifecycleStage(
		r.Context(),
		stageID,
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func nextLifecycleSortOrder(stages []db.LifecycleStage) int64 {
	next := int64(1)
	for _, s := range stages {
		if s.SortOrder >= next {
			next = s.SortOrder + 1
		}
	}
	return next
}

package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type EnvironmentHandler struct {
	repo *repository.Repository
}

func NewEnvironmentHandler(repo *repository.Repository) *EnvironmentHandler {
	return &EnvironmentHandler{repo: repo}
}

type environmentRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Tags        string `json:"tags"`
}

// swagger:route GET /environments environments listEnvironments
//
// List all environments.
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
//	  200: body:EnvironmentListResponse
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *EnvironmentHandler) ListEnvironments(
	w http.ResponseWriter,
	r *http.Request,
) {
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	envs, err := h.repo.Queries.ListEnvironmentsPaginated(
		r.Context(),
		db.ListEnvironmentsPaginatedParams{
			Limit:  limit,
			Offset: offset,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountEnvironments(r.Context())
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	items := make([]any, len(envs))
	for i, e := range envs {
		items[i] = e
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route POST /environments environments createEnvironment
//
// Create a new environment.
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
//	  201: body:Environment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *EnvironmentHandler) CreateEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	var req environmentRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	env, err := h.repo.Queries.CreateEnvironment(
		r.Context(),
		db.CreateEnvironmentParams{
			Name: name,
			Description: sql.NullString{
				String: req.Description,
				Valid:  req.Description != "",
			},
			Tags: sql.NullString{
				String: req.Tags,
				Valid:  req.Tags != "",
			},
		},
	)
	if err != nil {
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"An environment with this name already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusCreated, env)
}

// swagger:route GET /environments/{id} environments getEnvironment
//
// Get an environment by ID.
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
//	  200: body:Environment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *EnvironmentHandler) GetEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid environment ID")
		return
	}

	env, err := h.repo.Queries.GetEnvironment(r.Context(), id)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Environment not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, env)
}

// swagger:route PUT /environments/{id} environments updateEnvironment
//
// Update an environment.
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
//	  200: body:Environment
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  500: body:ServerError
func (h *EnvironmentHandler) UpdateEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid environment ID")
		return
	}

	var req environmentRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	env, err := h.repo.Queries.UpdateEnvironment(
		r.Context(),
		db.UpdateEnvironmentParams{
			ID:   id,
			Name: name,
			Description: sql.NullString{
				String: req.Description,
				Valid:  req.Description != "",
			},
			Tags: sql.NullString{
				String: req.Tags,
				Valid:  req.Tags != "",
			},
		},
	)
	if err != nil {
		if handler.IsUniqueViolation(err) {
			RespondError(
				w,
				http.StatusConflict,
				"An environment with this name already exists",
			)
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, env)
}

// swagger:route DELETE /environments/{id} environments deleteEnvironment
//
// Delete an environment.
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
func (h *EnvironmentHandler) DeleteEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid environment ID")
		return
	}

	if err := h.repo.Queries.DeleteEnvironment(r.Context(), id); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

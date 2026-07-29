package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"durpdeploy/internal/auth"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

type VariableHandler struct {
	repo *repository.Repository
}

func NewVariableHandler(repo *repository.Repository) *VariableHandler {
	return &VariableHandler{repo: repo}
}

type variableRequest struct {
	Name          string `json:"name"`
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
	Secret        bool   `json:"secret"`
}

type variableResponse struct {
	ID            int64  `json:"id"`
	ProjectID     int64  `json:"project_id"`
	Name          string `json:"name"`
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
	CreatedAt     int64  `json:"created_at"`
	Secret        int64  `json:"secret"`
}

func toVariableResponse(v db.Variable) variableResponse {
	var envID *int64
	if v.EnvironmentID.Valid {
		envID = &v.EnvironmentID.Int64
	}
	return variableResponse{
		ID:            v.ID,
		ProjectID:     v.ProjectID,
		Name:          v.Name,
		Value:         v.Value.String,
		EnvironmentID: envID,
		CreatedAt:     v.CreatedAt,
		Secret:        v.Secret,
	}
}

// swagger:route GET /projects/{id}/variables variables listVariables
//
// List variables for a project.
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
//	  200: body:VariableListResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *VariableHandler) ListVariables(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}
	limit, offset, ok := parsePagination(w, r)
	if !ok {
		return
	}

	var fEnvID sql.NullInt64
	if v := r.URL.Query().Get("environment_id"); v != "" {
		envID, err := strconv.ParseInt(v, 10, 64)
		if err != nil || envID <= 0 {
			RespondError(
				w,
				http.StatusBadRequest,
				"environment_id must be a positive integer",
			)
			return
		}
		fEnvID = sql.NullInt64{Int64: envID, Valid: true}
	}

	var fSecretOnly sql.NullInt64
	if v := r.URL.Query().Get("secret_only"); v != "" {
		switch v {
		case "true", "1":
			fSecretOnly = sql.NullInt64{Int64: 1, Valid: true}
		case "false", "0":
			fSecretOnly = sql.NullInt64{Int64: 0, Valid: true}
		default:
			RespondError(
				w,
				http.StatusBadRequest,
				"secret_only must be true or false",
			)
			return
		}
	}

	var fEnvArg any
	if fEnvID.Valid {
		fEnvArg = fEnvID.Int64
	}
	var fSecretArg any
	if fSecretOnly.Valid {
		fSecretArg = fSecretOnly.Int64
	}

	variables, err := h.repo.Queries.ListVariablesByProjectPaginated(
		r.Context(),
		db.ListVariablesByProjectPaginatedParams{
			ProjectID:      projectID,
			FEnvironmentID: fEnvArg,
			FSecretOnly:    fSecretArg,
			PageOffset:     offset,
			PageLimit:      limit,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	total, err := h.repo.Queries.CountVariablesByProject(
		r.Context(),
		db.CountVariablesByProjectParams{
			ProjectID:      projectID,
			FEnvironmentID: fEnvArg,
			FSecretOnly:    fSecretArg,
		},
	)
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := make([]variableResponse, len(variables))
	for i, v := range variables {
		resp[i] = toVariableResponse(v)
	}
	items := make([]any, len(resp))
	for i, rv := range resp {
		items[i] = rv
	}
	RespondJSON(w, http.StatusOK, PaginatedResponse{
		Items:  items,
		Total:  total,
		Limit:  limit,
		Offset: offset,
	})
}

// swagger:route POST /projects/{id}/variables variables createVariable
//
// Create a project variable.
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
//	  201: body:VariableResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  500: body:ServerError
func (h *VariableHandler) CreateVariable(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, ok := auth.ProjectIDFromContext(r.Context())
	if !ok {
		RespondError(w, http.StatusBadRequest, "Invalid project ID")
		return
	}

	var req variableRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	var envID sql.NullInt64
	if req.EnvironmentID != nil && *req.EnvironmentID > 0 {
		envID = sql.NullInt64{Int64: *req.EnvironmentID, Valid: true}
	}

	var secret int64
	if req.Secret {
		secret = 1
	}

	variable, err := h.repo.CreateVariable(r.Context(), db.CreateVariableParams{
		ProjectID: projectID,
		Name:      name,
		Value: sql.NullString{
			String: req.Value,
			Valid:  req.Value != "",
		},
		EnvironmentID: envID,
		Secret:        secret,
	})
	if err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusCreated, toVariableResponse(variable))
}

// swagger:route GET /projects/{id}/variables/{varId} variables getVariable
//
// Get a project variable.
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
//	  200: body:VariableResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *VariableHandler) GetVariable(w http.ResponseWriter, r *http.Request) {
	varID, err := parseParamInt(r, "varId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid variable ID")
		return
	}

	variable, err := h.repo.GetVariable(r.Context(), varID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Variable not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// R2: refuse to serve a variable that belongs to a different
	// project than the URL {id}. The decrypted value is the secret
	// the whole IDOR is about, so this is the must-pass check.
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	if variable.ProjectID != projectID {
		RespondError(w, http.StatusNotFound, "Variable not found")
		return
	}

	RespondJSON(w, http.StatusOK, toVariableResponse(variable))
}

// swagger:route PUT /projects/{id}/variables/{varId} variables updateVariable
//
// Update a project variable.
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
//	  200: body:VariableResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  404: body:NotFoundError
//	  500: body:ServerError
func (h *VariableHandler) UpdateVariable(
	w http.ResponseWriter,
	r *http.Request,
) {
	varID, err := parseParamInt(r, "varId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid variable ID")
		return
	}

	// R2: load first, then verify ownership. UpdateVariable returns
	// the row only on success, so the existence check has to happen
	// up front — and the secret-value row in the result set is what
	// we're protecting, so the project check goes before any write.
	existing, err := h.repo.Queries.GetVariable(r.Context(), varID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Variable not found")
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
		RespondError(w, http.StatusNotFound, "Variable not found")
		return
	}

	var req variableRequest
	if err := readJSON(r, &req); err != nil {
		RespondError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	name := trimSpace(req.Name)
	if name == "" {
		RespondError(w, http.StatusBadRequest, "Name is required")
		return
	}

	var envID sql.NullInt64
	if req.EnvironmentID != nil && *req.EnvironmentID > 0 {
		envID = sql.NullInt64{Int64: *req.EnvironmentID, Valid: true}
	}

	var secret int64
	if req.Secret {
		secret = 1
	}

	variable, err := h.repo.UpdateVariable(r.Context(), db.UpdateVariableParams{
		ID:   varID,
		Name: name,
		Value: sql.NullString{
			String: req.Value,
			Valid:  req.Value != "",
		},
		EnvironmentID: envID,
		Secret:        secret,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Variable not found")
			return
		}
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	RespondJSON(w, http.StatusOK, toVariableResponse(variable))
}

// swagger:route DELETE /projects/{id}/variables/{varId} variables deleteVariable
//
// Delete a project variable.
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
func (h *VariableHandler) DeleteVariable(
	w http.ResponseWriter,
	r *http.Request,
) {
	varID, err := parseParamInt(r, "varId")
	if err != nil {
		RespondError(w, http.StatusBadRequest, "Invalid variable ID")
		return
	}

	// R2: refuse to delete a variable that belongs to a different
	// project than the URL {id}. Without this check, a project-A
	// member could destroy project-B secrets by guessing IDs.
	existing, err := h.repo.Queries.GetVariable(r.Context(), varID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			RespondError(w, http.StatusNotFound, "Variable not found")
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
		RespondError(w, http.StatusNotFound, "Variable not found")
		return
	}

	if err := h.repo.Queries.DeleteVariable(r.Context(), varID); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/db"
	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

type LifecycleVariableHandler struct{ repo *repository.Repository }

func NewLifecycleVariableHandler(
	repo *repository.Repository,
) *LifecycleVariableHandler {
	return &LifecycleVariableHandler{repo: repo}
}

type lifecycleVariableResponse struct {
	ID            int64  `json:"id"`
	LifecycleID   int64  `json:"lifecycle_id"`
	Name          string `json:"name"`
	Value         string `json:"value"`
	EnvironmentID *int64 `json:"environment_id"`
	Secret        int64  `json:"secret"`
	CreatedAt     int64  `json:"created_at"`
}

func lifecycleVariableJSON(v db.LifecycleVariable) lifecycleVariableResponse {
	result := lifecycleVariableResponse{
		ID: v.ID, LifecycleID: v.LifecycleID, Name: v.Name,
		Secret: v.Secret, CreatedAt: v.CreatedAt,
	}
	if v.EnvironmentID.Valid {
		result.EnvironmentID = &v.EnvironmentID.Int64
	}
	if v.Secret == 0 {
		result.Value = v.Value.String
	}
	return result
}

func lifecycleVariableIDs(
	w http.ResponseWriter,
	r *http.Request,
) (db.GetLifecycleVariableParams, bool) {
	lifecycleID, err := parseParamInt(r, "id")
	if err != nil || lifecycleID <= 0 {
		RespondError(w, 400, "Invalid lifecycle ID")
		return db.GetLifecycleVariableParams{}, false
	}
	id, err := parseParamInt(r, "varId")
	if err != nil || id <= 0 {
		RespondError(w, 400, "Invalid variable ID")
		return db.GetLifecycleVariableParams{}, false
	}
	return db.GetLifecycleVariableParams{ID: id, LifecycleID: lifecycleID}, true
}

func respondLifecycleVariableError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows):
		RespondError(w, 404, "Lifecycle or variable not found")
	case errors.Is(err, repository.ErrVariableName),
		errors.Is(err, repository.ErrLifecycleVariableScope):
		RespondError(w, 422, err.Error())
	case handler.IsUniqueViolation(err):
		RespondError(
			w,
			422,
			"A variable with this name and scope already exists",
		)
	default:
		if status := handler.ArtifactErrorStatus(err); status != 0 {
			RespondError(w, status, err.Error())
		} else {
			RespondError(w, 500, "Unable to access lifecycle variables")
		}
	}
}

// swagger:route GET /lifecycles/{id}/variables variables listLifecycleVariables
//
// List shared lifecycle variables. Global admin only.
//
//	Produces:
//	- application/json
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:LifecycleVariableList
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *LifecycleVariableHandler) List(
	w http.ResponseWriter,
	r *http.Request,
) {
	id, err := parseParamInt(r, "id")
	if err != nil || id <= 0 {
		RespondError(w, 400, "Invalid lifecycle ID")
		return
	}
	if _, err := h.repo.Queries.GetLifecycle(r.Context(), id); err != nil {
		respondLifecycleVariableError(w, err)
		return
	}
	variables, err := h.repo.ListLifecycleVariables(r.Context(), id)
	if err != nil {
		respondLifecycleVariableError(w, err)
		return
	}
	result := make([]lifecycleVariableResponse, len(variables))
	for i, v := range variables {
		result[i] = lifecycleVariableJSON(v)
	}
	RespondJSON(w, 200, result)
}

// swagger:route GET /lifecycles/{id}/variables/{varId} variables getLifecycleVariable
//
// Get a masked shared lifecycle variable. Global admin only.
//
//	Produces:
//	- application/json
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:LifecycleVariable
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *LifecycleVariableHandler) Get(w http.ResponseWriter, r *http.Request) {
	ids, ok := lifecycleVariableIDs(w, r)
	if !ok {
		return
	}
	v, err := h.repo.GetLifecycleVariable(r.Context(), ids)
	if err != nil {
		respondLifecycleVariableError(w, err)
		return
	}
	RespondJSON(w, 200, lifecycleVariableJSON(v))
}

// swagger:route POST /lifecycles/{id}/variables variables createLifecycleVariable
//
// Create a shared lifecycle variable. Global admin only.
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//	Security:
//	  bearer:
//
//	Responses:
//	  201: body:LifecycleVariable
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *LifecycleVariableHandler) Create(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.save(w, r, false)
}

// swagger:route PUT /lifecycles/{id}/variables/{varId} variables updateLifecycleVariable
//
// Update a shared lifecycle variable. Blank values preserve existing secrets. Global admin only.
//
//	Consumes:
//	- application/json
//
//	Produces:
//	- application/json
//
//	Security:
//	  bearer:
//
//	Responses:
//	  200: body:LifecycleVariable
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *LifecycleVariableHandler) Update(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.save(w, r, true)
}

func (h *LifecycleVariableHandler) save(
	w http.ResponseWriter,
	r *http.Request,
	update bool,
) {
	lifecycleID, err := parseParamInt(r, "id")
	if err != nil || lifecycleID <= 0 {
		RespondError(w, 400, "Invalid lifecycle ID")
		return
	}
	var id int64
	if update {
		ids, ok := lifecycleVariableIDs(w, r)
		if !ok {
			return
		}
		id = ids.ID
	}
	var req variableRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	var env sql.NullInt64
	if req.EnvironmentID != nil {
		if *req.EnvironmentID <= 0 {
			RespondError(w, 400, "Invalid environment ID")
			return
		}
		env = sql.NullInt64{Int64: *req.EnvironmentID, Valid: true}
	}
	var secret int64
	if req.Secret {
		secret = 1
	}
	v, err := h.repo.SaveLifecycleVariable(
		r.Context(),
		repository.LifecycleVariableInput{
			ID: id, CreateLifecycleVariableParams: db.CreateLifecycleVariableParams{
				LifecycleID: lifecycleID, Name: req.Name,
				Value: sql.NullString{
					String: req.Value,
					Valid:  req.Value != "",
				},
				EnvironmentID: env, Secret: secret,
			},
		},
	)
	if err != nil {
		respondLifecycleVariableError(w, err)
		return
	}
	status := http.StatusCreated
	if update {
		status = http.StatusOK
	}
	RespondJSON(w, status, lifecycleVariableJSON(v))
}

// swagger:route DELETE /lifecycles/{id}/variables/{varId} variables deleteLifecycleVariable
//
// Delete a shared variable. Future deployments stop inheriting it; active deployments retain captured values. Global admin only.
//
//	Produces:
//	- application/json
//
//	Security:
//	  bearer:
//
//	Responses:
//	  204: body:EmptyResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *LifecycleVariableHandler) Delete(
	w http.ResponseWriter,
	r *http.Request,
) {
	ids, ok := lifecycleVariableIDs(w, r)
	if !ok {
		return
	}
	count, err := h.repo.Queries.DeleteLifecycleVariable(
		r.Context(),
		db.DeleteLifecycleVariableParams(ids),
	)
	if err != nil {
		respondLifecycleVariableError(w, err)
		return
	}
	if count == 0 {
		RespondError(w, 404, "Variable not found")
		return
	}
	w.WriteHeader(204)
}

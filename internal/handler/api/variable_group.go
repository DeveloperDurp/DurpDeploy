package api

import (
	"net/http"

	"durpdeploy/internal/handler"
	"durpdeploy/internal/repository"
)

// swagger:model VariableGroupValue
type variableGroupValue struct {
	// Required: true
	ID int64 `json:"id"`
	// Required: true
	Value *string `json:"value"`
}

// swagger:model VariableGroupRequest
type variableGroupRequest struct {
	// Required: true
	Overrides []variableGroupValue `json:"overrides"`
}

// swagger:parameters updateVariableGroup
//
//nolint:unused // Used by Swagger specification generation.
type updateVariableGroupParams struct {
	// in: path
	// Required: true
	ID int64 `json:"id"`
	// The ID of any environment override in the group.
	// in: path
	// Required: true
	VariableID int64 `json:"varId"`
	// in: body
	// Required: true
	Body variableGroupRequest
}

// swagger:route PUT /projects/{id}/variables/{varId}/group variables updateVariableGroup
//
// Save every editable environment override value for the same variable.
// A blank value preserves an existing secret.
//
//	Consumes:
//	- application/json
//	Security:
//	  bearer:
//	Responses:
//	  204: body:EmptyResponse
//	  400: body:BadRequestError
//	  401: body:UnauthorizedError
//	  403: body:ForbiddenError
//	  404: body:NotFoundError
//	  409: body:ConflictError
//	  413: body:RequestEntityTooLargeError
//	  422: body:ValidationError
//	  500: body:ServerError
func (h *VariableHandler) UpdateVariableGroup(
	w http.ResponseWriter, r *http.Request,
) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	anchorID, ok := parseIDParam(w, r, "varId")
	if !ok {
		return
	}
	if anchorID <= 0 {
		RespondError(w, http.StatusBadRequest, "Invalid variable ID")
		return
	}
	var req variableGroupRequest
	if !readJSONBool(w, r, &req) {
		return
	}
	values := make([]repository.VariableGroupValue, 0, len(req.Overrides))
	for _, override := range req.Overrides {
		if override.Value == nil {
			RespondError(
				w,
				http.StatusBadRequest,
				"Each override requires a value",
			)
			return
		}
		values = append(values, repository.VariableGroupValue{
			ID: override.ID, Value: *override.Value,
		})
	}
	err := h.repo.UpdateVariableGroup(r.Context(), projectID, anchorID, values)
	switch status := handler.VariableGroupErrorStatus(err); status {
	case http.StatusSeeOther:
		w.WriteHeader(http.StatusNoContent)
	case http.StatusNotFound:
		RespondError(w, status, variableNotFoundMessage)
	case http.StatusInternalServerError:
		RespondError(w, status, "Could not save variable values")
	default:
		RespondError(w, status, err.Error())
	}
}

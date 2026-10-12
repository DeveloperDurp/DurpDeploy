package api

import (
	"database/sql"
	"errors"
	"net/http"
)

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
//	  413: body:RequestEntityTooLargeError
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

	if err := h.repo.DeleteProjectVariable(
		r.Context(), existing, r.URL.Query().Get("reset") == "inherit",
	); err != nil {
		RespondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

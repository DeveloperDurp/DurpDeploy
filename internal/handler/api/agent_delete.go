package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/repository"
)

// swagger:route DELETE /admin/agents/{id} admin-agents deleteAgent
//
// Delete an agent from the inventory, retaining deployment history.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	204: body:EmptyResponse
//	400: body:BadRequestError
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	409: body:ConflictError
//	413: body:RequestEntityTooLargeError
//	500: body:ServerError
func (h *AgentHandler) DeleteAgent(w http.ResponseWriter, r *http.Request) {
	err := h.repo.DeleteAgent(r.Context(), chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		RespondError(w, http.StatusNotFound, "Agent not found")
	case errors.Is(err, repository.ErrAgentDeletionBlocked):
		RespondError(w, http.StatusConflict, err.Error())
	case err != nil:
		RespondError(
			w,
			http.StatusInternalServerError,
			"Could not delete agent",
		)
	default:
		w.WriteHeader(http.StatusNoContent)
	}
}

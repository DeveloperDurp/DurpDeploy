package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/repository"
	"github.com/go-chi/chi/v5"
)

// swagger:route POST /admin/agents/{id}/drain admin-agents drainAgent
//
// Stop new claims without cancelling issued work. Repeated calls are harmless.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	400: body:BadRequestError
//	413: body:RequestEntityTooLargeError
//	204: body:EmptyResponse
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	409: body:ConflictError
//	500: body:ServerError
func (h *AgentHandler) DrainAgent(w http.ResponseWriter, r *http.Request) {
	h.setDrain(w, r, true)
}

// swagger:route POST /admin/agents/{id}/resume admin-agents resumeAgent
//
// Resume new claims. Repeated calls are harmless.
//
// Security:
//
//	bearer:
//
// Responses:
//
//	400: body:BadRequestError
//	413: body:RequestEntityTooLargeError
//	204: body:EmptyResponse
//	401: body:UnauthorizedError
//	403: body:ForbiddenError
//	404: body:NotFoundError
//	409: body:ConflictError
//	500: body:ServerError
func (h *AgentHandler) ResumeAgent(w http.ResponseWriter, r *http.Request) {
	h.setDrain(w, r, false)
}

func (h *AgentHandler) setDrain(
	w http.ResponseWriter,
	r *http.Request,
	draining bool,
) {
	changed, err := h.repo.SetAgentDraining(
		r.Context(), chi.URLParam(r, "id"), draining,
	)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			RespondError(w, http.StatusNotFound, "Agent not found")
		case errors.Is(err, repository.ErrAgentUnavailable):
			RespondError(
				w,
				http.StatusConflict,
				"Agent is not active and paired",
			)
		default:
			RespondError(
				w,
				http.StatusInternalServerError,
				"Could not change drain state",
			)
		}
		return
	}
	if !changed {
		audit.Suppress(r)
	}
	w.WriteHeader(http.StatusNoContent)
}

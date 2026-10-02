package api

import (
	"net/http"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/db"
	"github.com/go-chi/chi/v5"
)

type agentEnvironmentRequest struct {
	EnvironmentID int64 `json:"environment_id"`
}

// swagger:route POST /admin/agents/{id}/environments admin-agents addAgentEnvironment
//
// Assign an environment routing label to an agent.
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
//	500: body:ServerError
func (h *AgentHandler) AddEnvironment(w http.ResponseWriter, r *http.Request) {
	h.setEnvironment(w, r, true)
}

// swagger:route DELETE /admin/agents/{id}/environments admin-agents deleteAgentEnvironment
//
// Remove an environment routing label from an agent.
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
//	500: body:ServerError
func (h *AgentHandler) DeleteEnvironment(
	w http.ResponseWriter,
	r *http.Request,
) {
	h.setEnvironment(w, r, false)
}

func (h *AgentHandler) setEnvironment(
	w http.ResponseWriter,
	r *http.Request,
	assigned bool,
) {
	var input agentEnvironmentRequest
	if !readJSONBool(w, r, &input) {
		return
	}
	if input.EnvironmentID <= 0 {
		RespondError(
			w,
			http.StatusBadRequest,
			"environment_id must be positive",
		)
		return
	}
	id := chi.URLParam(r, "id")
	if _, err := h.repo.Queries.GetAgent(r.Context(), id); err != nil {
		writeAgentNotFound(w, err)
		return
	}
	if _, err := h.repo.Queries.GetEnvironment(
		r.Context(), input.EnvironmentID,
	); err != nil {
		writeAgentNotFound(w, err)
		return
	}
	var changed int64
	var err error
	if assigned {
		changed, err = h.repo.Queries.AddAgentEnvironmentLabel(
			r.Context(), db.AddAgentEnvironmentLabelParams{
				AgentID: id, EnvironmentID: input.EnvironmentID,
			},
		)
	} else {
		changed, err = h.repo.Queries.DeleteAgentEnvironmentLabel(
			r.Context(), db.DeleteAgentEnvironmentLabelParams{
				AgentID: id, EnvironmentID: input.EnvironmentID,
			},
		)
	}
	if err != nil {
		RespondError(
			w,
			http.StatusInternalServerError,
			"Could not change environment label",
		)
		return
	}
	if changed == 0 {
		if !assigned {
			RespondError(
				w,
				http.StatusNotFound,
				"Agent environment label not found",
			)
			return
		}
		audit.Suppress(r)
	}
	audit.SetAgentAssignment(r, "", input.EnvironmentID)
	w.WriteHeader(http.StatusNoContent)
}

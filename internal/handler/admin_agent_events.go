package handler

import (
	"database/sql"
	"net/http"
)

type agentEventResponse struct {
	ID            int64   `json:"id"`
	AgentID       *string `json:"agent_id,omitempty"`
	DeploymentID  *int64  `json:"deployment_id,omitempty"`
	EventType     string  `json:"event_type"`
	DispatchState *string `json:"dispatch_state,omitempty"`
	CreatedAt     int64   `json:"created_at"`
}

func (h *AgentAdminHandler) ListAgentEvents(
	w http.ResponseWriter,
	r *http.Request,
) {
	agent, ok := h.agent(w, r)
	if !ok {
		return
	}
	events, err := h.repo.Queries.ListRedactedAgentEventsByAgent(
		r.Context(),
		sql.NullString{String: agent.ID, Valid: true},
	)
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not list agent events",
		)
		return
	}
	response := make([]agentEventResponse, len(events))
	for index, event := range events {
		response[index] = agentEventResponse{
			ID:            event.ID,
			AgentID:       nullableString(event.AgentID),
			DeploymentID:  nullableInt64(event.DeploymentID),
			EventType:     event.EventType,
			DispatchState: nullableString(event.DispatchState),
			CreatedAt:     event.CreatedAt,
		}
	}
	writeAdminJSON(w, http.StatusOK, response)
}

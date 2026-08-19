package handler

import (
	"database/sql"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/agentproto"
	"durpdeploy/internal/db"
)

type agentTagRequest struct {
	Value string `json:"value"`
}

type agentTagResponse struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	CreatedAt int64  `json:"created_at"`
}

type agentEventResponse struct {
	ID            int64   `json:"id"`
	AgentID       *string `json:"agent_id,omitempty"`
	DeploymentID  *int64  `json:"deployment_id,omitempty"`
	EventType     string  `json:"event_type"`
	DispatchState *string `json:"dispatch_state,omitempty"`
	CreatedAt     int64   `json:"created_at"`
}

func (h *AgentAdminHandler) ListAgentTags(
	w http.ResponseWriter,
	r *http.Request,
) {
	if _, ok := h.agent(w, r); !ok {
		return
	}
	tags, err := h.repo.Queries.ListAgentTagsByAgent(
		r.Context(),
		chi.URLParam(r, "agentID"),
	)
	if err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not list agent tags",
		)
		return
	}
	response := make([]agentTagResponse, len(tags))
	for index, tag := range tags {
		response[index] = agentTagResponse{
			Key:       tag.TagKey,
			Value:     tag.TagValue,
			CreatedAt: tag.CreatedAt,
		}
	}
	writeAdminJSON(w, http.StatusOK, response)
}

func (h *AgentAdminHandler) SetAgentTag(
	w http.ResponseWriter,
	r *http.Request,
) {
	if _, ok := h.agent(w, r); !ok {
		return
	}
	var request agentTagRequest
	if !decodeAdminJSON(w, r, &request) {
		return
	}
	key, value, ok := parseAgentTag(chi.URLParam(r, "tagKey"), request.Value)
	if !ok {
		writeAdminError(w, http.StatusUnprocessableEntity, "invalid agent tag")
		return
	}
	if err := h.repo.Queries.SetAgentTag(r.Context(), db.SetAgentTagParams{
		AgentID:  chi.URLParam(r, "agentID"),
		TagKey:   key,
		TagValue: value,
	}); err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not set agent tag",
		)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AgentAdminHandler) SetAgentTagForm(
	w http.ResponseWriter,
	r *http.Request,
) {
	if _, ok := h.agentPageAgent(w, r); !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid agent tag form", http.StatusBadRequest)
		return
	}
	key, value, ok := parseAgentTag(r.FormValue("key"), r.FormValue("value"))
	if !ok {
		http.Error(w, "invalid agent tag", http.StatusUnprocessableEntity)
		return
	}
	agentID := chi.URLParam(r, "agentID")
	if err := h.repo.Queries.SetAgentTag(r.Context(), db.SetAgentTagParams{
		AgentID: agentID, TagKey: key, TagValue: value,
	}); err != nil {
		http.Error(w, "could not set agent tag", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/agents/"+agentID, http.StatusSeeOther)
}

func (h *AgentAdminHandler) DeleteAgentTag(
	w http.ResponseWriter,
	r *http.Request,
) {
	if _, ok := h.agent(w, r); !ok {
		return
	}
	key, _, ok := parseAgentTag(chi.URLParam(r, "tagKey"), "value")
	if !ok {
		writeAdminError(
			w,
			http.StatusUnprocessableEntity,
			"invalid agent tag key",
		)
		return
	}
	if err := h.repo.Queries.DeleteAgentTag(
		r.Context(),
		db.DeleteAgentTagParams{
			AgentID: chi.URLParam(r, "agentID"),
			TagKey:  key,
		},
	); err != nil {
		writeAdminError(
			w,
			http.StatusInternalServerError,
			"could not delete agent tag",
		)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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

func parseAgentTag(rawKey, rawValue string) (string, string, bool) {
	key := strings.TrimSpace(rawKey)
	value := strings.TrimSpace(rawValue)
	selector, err := agentproto.ParseSelector(key + "=" + value)
	if err != nil {
		return "", "", false
	}
	entries := selector.Entries()
	if len(entries) != 1 {
		return "", "", false
	}
	return string(entries[0].Key), string(entries[0].Value), true
}

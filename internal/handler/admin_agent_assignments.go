package handler

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
)

func (h *AgentAdminHandler) AssignEnvironment(
	writer http.ResponseWriter,
	request *http.Request,
) {
	agent, ok := h.agentPageAgent(writer, request)
	if !ok {
		return
	}
	if agent.Status != "active" {
		http.Error(writer, "agent must be active", http.StatusConflict)
		return
	}
	pairing, err := h.repo.Queries.GetAgentPairing(
		request.Context(),
		agent.ID,
	)
	if err != nil || pairing.State != "paired" {
		http.Error(writer, "agent must be paired", http.StatusConflict)
		return
	}
	if err := request.ParseForm(); err != nil {
		http.Error(writer, "invalid assignment form", http.StatusBadRequest)
		return
	}
	environmentID, err := strconv.ParseInt(
		request.FormValue("environment_id"),
		10,
		64,
	)
	if err != nil || environmentID < 1 {
		http.Error(
			writer,
			"invalid environment",
			http.StatusUnprocessableEntity,
		)
		return
	}
	if _, err := h.repo.Queries.GetEnvironment(
		request.Context(),
		environmentID,
	); err != nil {
		http.Error(writer, "environment not found", http.StatusNotFound)
		return
	}
	if _, err := h.repo.Queries.AssignAgentToEnvironment(
		request.Context(),
		db.AssignAgentToEnvironmentParams{
			EnvironmentID: environmentID,
			AgentID:       agent.ID,
		},
	); err != nil {
		http.Error(
			writer,
			"could not assign agent",
			http.StatusInternalServerError,
		)
		return
	}
	http.Redirect(
		writer,
		request,
		"/admin/agents/"+chi.URLParam(request, "agentID"),
		http.StatusSeeOther,
	)
}

func (h *AgentAdminHandler) RePairAgent(
	writer http.ResponseWriter,
	request *http.Request,
) {
	agent, ok := h.agentPageAgent(writer, request)
	if !ok {
		return
	}
	updated, err := h.repo.Queries.RePairAgent(
		request.Context(),
		db.RePairAgentParams{
			ID: agent.ID, UpdatedAt: time.Now().Unix(),
		},
	)
	if err != nil {
		http.Error(
			writer,
			"could not prepare agent re-pairing",
			http.StatusInternalServerError,
		)
		return
	}
	if updated != 1 {
		http.Error(writer, "agent must be revoked", http.StatusConflict)
		return
	}
	http.Redirect(
		writer,
		request,
		"/admin/agents/"+agent.ID,
		http.StatusSeeOther,
	)
}

package handler

import (
	"net/http"

	"durpdeploy/views/pages"
)

func (h *AgentsHandler) renderAssignments(
	w http.ResponseWriter,
	r *http.Request,
	agentID string,
) {
	if r.Header.Get("HX-Target") == "environments-list" {
		environments, err := h.repo.Queries.ListEnvironments(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		rows, agents, err := NewEnvironmentHandler(h.repo).
			environmentAgentRows(r, environments)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := pages.EnvironmentsListFragment(rows, agents).
			Render(r.Context(), w); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
		return
	}
	agent, err := h.repo.Queries.GetAgent(r.Context(), agentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	assigned, err := h.repo.Queries.ListAgentEnvironments(r.Context(), agentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	environments, err := h.repo.Queries.ListEnvironments(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := pages.AgentAssignments(agent, assigned, environments).
		Render(r.Context(), w); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

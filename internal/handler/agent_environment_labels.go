package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
)

func (h *AgentsHandler) AddEnvironmentLabel(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	agentID := chi.URLParam(r, "id")
	environmentID, err := strconv.ParseInt(
		r.FormValue("environment_id"),
		10,
		64,
	)
	if err != nil {
		http.Error(w, "Invalid environment ID", http.StatusBadRequest)
		return
	}
	if _, err := h.repo.Queries.GetAgent(r.Context(), agentID); err != nil {
		writeAgentEnvironmentLabelNotFound(w, r, err)
		return
	}
	if _, err := h.repo.Queries.GetEnvironment(
		r.Context(),
		environmentID,
	); err != nil {
		writeAgentEnvironmentLabelNotFound(w, r, err)
		return
	}
	if _, err := h.repo.Queries.AddAgentEnvironmentLabel(
		r.Context(),
		db.AddAgentEnvironmentLabelParams{
			AgentID: agentID, EnvironmentID: environmentID,
		},
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/agents/"+agentID, http.StatusSeeOther)
}

func (h *AgentsHandler) DeleteEnvironmentLabel(
	w http.ResponseWriter,
	r *http.Request,
) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	agentID := chi.URLParam(r, "id")
	environmentID, err := strconv.ParseInt(
		r.FormValue("environment_id"),
		10,
		64,
	)
	if err != nil {
		http.Error(w, "Invalid environment ID", http.StatusBadRequest)
		return
	}
	changed, err := h.repo.Queries.DeleteAgentEnvironmentLabel(
		r.Context(),
		db.DeleteAgentEnvironmentLabelParams{
			AgentID: agentID, EnvironmentID: environmentID,
		},
	)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if changed == 0 {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/admin/agents/"+agentID, http.StatusSeeOther)
}

func writeAgentEnvironmentLabelNotFound(
	w http.ResponseWriter,
	r *http.Request,
	err error,
) {
	if errors.Is(err, sql.ErrNoRows) {
		http.NotFound(w, r)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
}

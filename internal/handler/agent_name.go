package handler

import (
	"database/sql"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/db"
)

func (h *AgentsHandler) UpdateName(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.FormValue("name"))
	if name == "" || len(name) > 255 {
		http.Error(
			w,
			"Agent name must be between 1 and 255 characters",
			http.StatusUnprocessableEntity,
		)
		return
	}
	agent, err := h.repo.Queries.GetAgent(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if _, err := h.repo.Queries.UpdateAgent(
		r.Context(),
		db.UpdateAgentParams{Name: name, Endpoint: agent.Endpoint, ID: agent.ID},
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "Revoked agents cannot be edited", http.StatusConflict)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/admin/agents/"+agent.ID, http.StatusSeeOther)
}

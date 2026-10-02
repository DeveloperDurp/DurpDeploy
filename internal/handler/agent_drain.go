package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/repository"
	"github.com/go-chi/chi/v5"
)

func (h *AgentsHandler) Drain(w http.ResponseWriter, r *http.Request) {
	h.setDrain(w, r, true)
}

func (h *AgentsHandler) Resume(w http.ResponseWriter, r *http.Request) {
	h.setDrain(w, r, false)
}

func (h *AgentsHandler) setDrain(
	w http.ResponseWriter,
	r *http.Request,
	draining bool,
) {
	id := chi.URLParam(r, "id")
	changed, err := h.repo.SetAgentDraining(r.Context(), id, draining)
	if err != nil {
		switch {
		case errors.Is(err, sql.ErrNoRows):
			http.NotFound(w, r)
		case errors.Is(err, repository.ErrAgentUnavailable):
			http.Error(
				w,
				"Agent cannot change drain state",
				http.StatusConflict,
			)
		default:
			http.Error(
				w,
				"Could not change drain state",
				http.StatusInternalServerError,
			)
		}
		return
	}
	if !changed {
		audit.Suppress(r)
	}
	http.Redirect(w, r, "/admin/agents/"+id, http.StatusSeeOther)
}

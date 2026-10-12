package handler

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/repository"
)

func (h *AgentsHandler) Delete(w http.ResponseWriter, r *http.Request) {
	err := h.repo.DeleteAgent(r.Context(), chi.URLParam(r, "id"))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case errors.Is(err, repository.ErrAgentDeletionBlocked):
		if r.Header.Get("HX-Request") == "true" {
			audit.Suppress(r)
			SetToastError(w, err.Error())
			w.Header().Set("HX-Reswap", "none")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Error(w, err.Error(), http.StatusConflict)
	case err != nil:
		http.Error(w, "Could not delete agent", http.StatusInternalServerError)
	default:
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", "/admin/agents")
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/admin/agents", http.StatusSeeOther)
	}
}

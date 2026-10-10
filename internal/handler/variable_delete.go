package handler

import (
	"database/sql"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
)

func (h *VariableHandler) DeleteVariable(
	w http.ResponseWriter,
	r *http.Request,
) {
	projectID, err := parseProjectID(r)
	if err != nil {
		http.Error(w, "Invalid project ID", http.StatusBadRequest)
		return
	}

	varIDStr := chi.URLParam(r, "varId")
	varID, err := strconv.ParseInt(varIDStr, 10, 64)
	if err != nil {
		http.Error(w, "Invalid variable ID", http.StatusBadRequest)
		return
	}
	variable, err := h.getProjectVariable(r, projectID, varID)
	if err != nil {
		if err == sql.ErrNoRows {
			http.NotFound(w, r)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	if err := h.repo.DeleteProjectVariable(
		r.Context(), variable, r.URL.Query().Get("reset") == "inherit",
	); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	h.renderVariablesFragment(w, r, projectID)
}

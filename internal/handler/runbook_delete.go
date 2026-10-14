package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

func (h *RunbookHandler) Delete(w http.ResponseWriter, r *http.Request) {
	project, book, ok := h.book(w, r)
	if !ok {
		return
	}
	book, err := h.repo.DeleteRunbook(r.Context(), db.GetRunbookParams{
		ID: book.ID, ProjectID: project.ID,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		http.NotFound(w, r)
	case errors.Is(err, repository.ErrRunbookHasActiveExecution):
		message := repository.ErrRunbookHasActiveExecution.Error()
		if r.Header.Get("HX-Request") == "true" {
			SetToastError(w, message)
			w.Header().Set("HX-Reswap", "none")
		}
		http.Error(w, message, http.StatusConflict)
	case err != nil:
		http.Error(w, "Cannot delete runbook", http.StatusInternalServerError)
	default:
		audit.SetRunbookDeletion(r, book)
		list := fmt.Sprintf("/projects/%d/runbooks", project.ID)
		if r.Header.Get("HX-Request") == "true" {
			w.Header().Set("HX-Redirect", list)
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, list, http.StatusSeeOther)
	}
}

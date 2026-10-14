package api

import (
	"database/sql"
	"errors"
	"net/http"

	"durpdeploy/internal/audit"
	"durpdeploy/internal/db"
	"durpdeploy/internal/repository"
)

// swagger:route DELETE /projects/{id}/runbooks/{runbookId} runbooks deleteRunbook
// Delete a runbook from active lists and disable its schedules.
//
// Saved versions, backing releases, artifact pins, completed executions, and
// logs remain for history. Future execution and retry are blocked.
// Deleted names remain reserved. Finish or cancel unfinished executions and
// confirm cleanup before deletion. A repeated deletion returns 404.
//
// Responses:
// 204: description: Runbook deleted
// 400: body:BadRequestError
// 401: body:UnauthorizedError
// 403: body:ForbiddenError
// 404: body:NotFoundError
// 409: body:ConflictError
// 413: body:RequestEntityTooLargeError
// 500: body:ServerError
func (h *RunbookHandler) Delete(w http.ResponseWriter, r *http.Request) {
	projectID, ok := requireProjectFromContext(w, r)
	if !ok {
		return
	}
	id, ok := parseIDParam(w, r, "runbookId")
	if !ok {
		return
	}
	book, err := h.repo.DeleteRunbook(r.Context(), db.GetRunbookParams{
		ID: id, ProjectID: projectID,
	})
	switch {
	case errors.Is(err, sql.ErrNoRows):
		RespondError(w, http.StatusNotFound, "Runbook not found")
	case errors.Is(err, repository.ErrRunbookHasActiveExecution):
		RespondError(
			w,
			http.StatusConflict,
			repository.ErrRunbookHasActiveExecution.Error(),
		)
	case err != nil:
		RespondError(w, http.StatusInternalServerError, "Cannot delete runbook")
	default:
		audit.SetRunbookDeletion(r, book)
		w.WriteHeader(http.StatusNoContent)
	}
}

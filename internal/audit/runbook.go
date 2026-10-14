package audit

import (
	"database/sql"
	"net/http"

	"durpdeploy/internal/db"
)

// SetRunbookDeletion keeps the persisted identity for the middleware audit.
func SetRunbookDeletion(r *http.Request, book db.Runbook) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok {
		state.deletedRunbook = book
		state.overridden = &actionOverride{
			action: "delete_runbook", entityType: "runbook",
			entityID: sql.NullInt64{Int64: book.ID, Valid: true},
		}
	}
}

func addRunbookDeletionDetails(r *http.Request, details map[string]any) {
	if state, ok := r.Context().Value(requestStateKey{}).(*requestState); ok &&
		state.deletedRunbook.ID != 0 {
		details["project_id"] = state.deletedRunbook.ProjectID
		details["runbook_id"] = state.deletedRunbook.ID
		details["name"] = state.deletedRunbook.Name
	}
}
